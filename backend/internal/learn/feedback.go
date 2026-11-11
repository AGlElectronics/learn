package learn

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

const maxFeedbackComment = 4000

// SetFeedbackPepper sets the secret (LEARN_FEEDBACK_PEPPER) that keys the
// anonymous-feedback dedupe hash, so the hash can't be recomputed from ids
// that appear elsewhere in the database. Changing it lets everyone give
// anonymous feedback once more. Call before serving requests.
func (s *Service) SetFeedbackPepper(pepper string) {
	s.feedbackPepper = []byte(pepper)
}

// FeedbackSettings is a course's feedback switch.
type FeedbackSettings struct {
	Enabled   bool `json:"enabled"`
	Anonymous bool `json:"anonymous"`
}

// feedbackHash identifies one person's feedback on one target without
// storing who it was. activityID is nil for course feedback.
func (s *Service) feedbackHash(courseID uuid.UUID, activityID *uuid.UUID, userID uuid.UUID) string {
	act := ""
	if activityID != nil {
		act = activityID.String()
	}
	mac := hmac.New(sha256.New, s.feedbackPepper)
	mac.Write([]byte(courseID.String() + "|" + act + "|" + userID.String()))
	return hex.EncodeToString(mac.Sum(nil))
}

// GetFeedbackSettings reads a course's feedback settings.
func (s *Service) GetFeedbackSettings(ctx context.Context, courseID uuid.UUID) (FeedbackSettings, error) {
	var fs FeedbackSettings
	err := s.pool.QueryRow(ctx, `SELECT feedback_enabled, feedback_anonymous FROM courses WHERE id = $1`, courseID).
		Scan(&fs.Enabled, &fs.Anonymous)
	if errors.Is(err, pgx.ErrNoRows) {
		return fs, httperror.NotFound("course")
	}
	return fs, err
}

// SetFeedbackSettings changes the feedback switches; nil = unchanged.
// Responses already given keep the identity they were stored with.
func (s *Service) SetFeedbackSettings(ctx context.Context, courseID uuid.UUID, enabled, anonymous *bool) (FeedbackSettings, error) {
	var fs FeedbackSettings
	err := s.pool.QueryRow(ctx, `
		UPDATE courses
		   SET feedback_enabled = COALESCE($2, feedback_enabled),
		       feedback_anonymous = COALESCE($3, feedback_anonymous),
		       updated_at = now()
		 WHERE id = $1
		 RETURNING feedback_enabled, feedback_anonymous`, courseID, enabled, anonymous).Scan(&fs.Enabled, &fs.Anonymous)
	if errors.Is(err, pgx.ErrNoRows) {
		return fs, httperror.NotFound("course")
	}
	return fs, err
}

// FeedbackTarget is one thing a learner can rate and their state on it.
type FeedbackTarget struct {
	// ActivityID is nil for the course itself.
	ActivityID *uuid.UUID `json:"activity_id"`
	Title      string     `json:"title"`
	Eligible   bool       `json:"eligible"`
	Submitted  bool       `json:"submitted"`
}

// MyFeedback is what the course player needs to prompt for feedback.
type MyFeedback struct {
	FeedbackSettings
	Course   FeedbackTarget   `json:"course"`
	Sessions []FeedbackTarget `json:"sessions"`
}

// courseFinished: every activity of the course is complete for userID.
func (s *Service) courseFinished(ctx context.Context, courseID, userID uuid.UUID) (bool, error) {
	p, err := s.progressFor(ctx, courseID, userID)
	if err != nil {
		return false, err
	}
	return p.Total > 0 && p.Completed >= p.Total, nil
}

// MyFeedback reports, for one learner, which targets they may rate now and
// which they already rated. Sessions are listed in course order.
func (s *Service) MyFeedback(ctx context.Context, courseID, userID uuid.UUID) (MyFeedback, error) {
	out := MyFeedback{Sessions: []FeedbackTarget{}}
	var title string
	err := s.pool.QueryRow(ctx, `SELECT title, feedback_enabled, feedback_anonymous FROM courses WHERE id = $1`, courseID).
		Scan(&title, &out.Enabled, &out.Anonymous)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, httperror.NotFound("course")
	}
	if err != nil {
		return out, err
	}
	finished, err := s.courseFinished(ctx, courseID, userID)
	if err != nil {
		return out, err
	}
	out.Course = FeedbackTarget{Title: title, Eligible: finished}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title,
		       EXISTS (SELECT 1 FROM session_signups su
		                WHERE su.activity_id = a.id AND su.user_id = $2 AND su.status = 'attended')
		  FROM activities a JOIN sections s ON s.id = a.section_id
		 WHERE s.course_id = $1 AND a.kind = $3
		 ORDER BY s.position, s.created_at, a.position, a.created_at`, courseID, userID, domain.ActivitySession)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t FeedbackTarget
		var id uuid.UUID
		if err := rows.Scan(&id, &t.Title, &t.Eligible); err != nil {
			rows.Close()
			return out, err
		}
		t.ActivityID = &id
		out.Sessions = append(out.Sessions, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	hashes := []string{s.feedbackHash(courseID, nil, userID)}
	for _, t := range out.Sessions {
		hashes = append(hashes, s.feedbackHash(courseID, t.ActivityID, userID))
	}
	rows, err = s.pool.Query(ctx, `SELECT dedupe_hash FROM feedback WHERE dedupe_hash = ANY($1)`, hashes)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	done := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return out, err
		}
		done[h] = true
	}
	out.Course.Submitted = done[hashes[0]]
	for i := range out.Sessions {
		out.Sessions[i].Submitted = done[hashes[i+1]]
	}
	return out, rows.Err()
}

// FeedbackInput is one learner's rating.
type FeedbackInput struct {
	ActivityID *uuid.UUID
	Rating     int
	Comment    string
}

// SubmitFeedback stores a rating on the course (after finishing it) or on
// a session (after attending it). 403 while feedback is off or the learner
// isn't eligible yet; 409 on a second rating of the same target. Anonymous
// courses never store the user id.
func (s *Service) SubmitFeedback(ctx context.Context, courseID, userID uuid.UUID, in FeedbackInput) error {
	if in.Rating < 1 || in.Rating > 5 {
		return httperror.Validation("rating must be between 1 and 5", nil)
	}
	in.Comment = strings.TrimSpace(in.Comment)
	if len(in.Comment) > maxFeedbackComment {
		return httperror.Validation("the comment is too long", nil)
	}
	fs, err := s.GetFeedbackSettings(ctx, courseID)
	if err != nil {
		return err
	}
	if !fs.Enabled {
		return httperror.New(httperror.CodeForbidden, "this course doesn't collect feedback")
	}
	if in.ActivityID == nil {
		finished, err := s.courseFinished(ctx, courseID, userID)
		if err != nil {
			return err
		}
		if !finished {
			return httperror.New(httperror.CodeForbidden, "finish the course before rating it")
		}
	} else {
		var kind string
		var attended bool
		err := s.pool.QueryRow(ctx, `
			SELECT a.kind,
			       EXISTS (SELECT 1 FROM session_signups su
			                WHERE su.activity_id = a.id AND su.user_id = $3 AND su.status = 'attended')
			  FROM activities a JOIN sections s ON s.id = a.section_id
			 WHERE a.id = $1 AND s.course_id = $2`, *in.ActivityID, courseID, userID).Scan(&kind, &attended)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind != domain.ActivitySession) {
			return httperror.Validation("feedback can be given on the course or on one of its sessions", nil)
		}
		if err != nil {
			return err
		}
		if !attended {
			return httperror.New(httperror.CodeForbidden, "attend the session before rating it")
		}
	}
	var uid *uuid.UUID
	if !fs.Anonymous {
		uid = &userID
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO feedback (course_id, activity_id, user_id, dedupe_hash, rating, comment)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		courseID, in.ActivityID, uid, s.feedbackHash(courseID, in.ActivityID, userID), in.Rating, in.Comment)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return httperror.Conflict("you have already given feedback on this")
	}
	return err
}

// FeedbackComment is one response with a comment, newest first. Name is
// empty for anonymous responses.
type FeedbackComment struct {
	Rating      int       `json:"rating"`
	Comment     string    `json:"comment"`
	DisplayName string    `json:"display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// FeedbackGroup is the summary for the course or one session.
type FeedbackGroup struct {
	ActivityID *uuid.UUID `json:"activity_id"`
	Title      string     `json:"title"`
	Count      int        `json:"count"`
	// Average is nil without responses.
	Average  *float64          `json:"average"`
	Comments []FeedbackComment `json:"comments"`
}

// FeedbackSummary is the staff view of a course's feedback.
type FeedbackSummary struct {
	FeedbackSettings
	Course   FeedbackGroup   `json:"course"`
	Sessions []FeedbackGroup `json:"sessions"`
}

// FeedbackSummary returns average rating, response count and comments for
// the course and each of its sessions.
func (s *Service) FeedbackSummary(ctx context.Context, courseID uuid.UUID) (FeedbackSummary, error) {
	out := FeedbackSummary{Sessions: []FeedbackGroup{}}
	var title string
	err := s.pool.QueryRow(ctx, `SELECT title, feedback_enabled, feedback_anonymous FROM courses WHERE id = $1`, courseID).
		Scan(&title, &out.Enabled, &out.Anonymous)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, httperror.NotFound("course")
	}
	if err != nil {
		return out, err
	}
	out.Course = FeedbackGroup{Title: title, Comments: []FeedbackComment{}}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title
		  FROM activities a JOIN sections s ON s.id = a.section_id
		 WHERE s.course_id = $1 AND a.kind = $2
		 ORDER BY s.position, s.created_at, a.position, a.created_at`, courseID, domain.ActivitySession)
	if err != nil {
		return out, err
	}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		g := FeedbackGroup{Comments: []FeedbackComment{}}
		if err := rows.Scan(&id, &g.Title); err != nil {
			rows.Close()
			return out, err
		}
		g.ActivityID = &id
		index[id] = len(out.Sessions)
		out.Sessions = append(out.Sessions, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	group := func(activityID *uuid.UUID) *FeedbackGroup {
		if activityID == nil {
			return &out.Course
		}
		if i, ok := index[*activityID]; ok {
			return &out.Sessions[i]
		}
		return nil
	}

	rows, err = s.pool.Query(ctx, `
		SELECT activity_id, count(*), avg(rating)::float8
		  FROM feedback WHERE course_id = $1 GROUP BY activity_id`, courseID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var aid *uuid.UUID
		var n int
		var avg float64
		if err := rows.Scan(&aid, &n, &avg); err != nil {
			rows.Close()
			return out, err
		}
		if g := group(aid); g != nil {
			g.Count, g.Average = n, &avg
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT f.activity_id, f.rating, f.comment, COALESCE(u.display_name, ''), f.created_at
		  FROM feedback f LEFT JOIN users u ON u.id = f.user_id
		 WHERE f.course_id = $1 AND f.comment <> ''
		 ORDER BY f.created_at DESC`, courseID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid *uuid.UUID
		var c FeedbackComment
		if err := rows.Scan(&aid, &c.Rating, &c.Comment, &c.DisplayName, &c.CreatedAt); err != nil {
			return out, err
		}
		if g := group(aid); g != nil {
			g.Comments = append(g.Comments, c)
		}
	}
	return out, rows.Err()
}
