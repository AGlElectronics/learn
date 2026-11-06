package learn

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// QuestionCategory groups questions in a course's bank. Quizzes can draw
// random questions from a category.
type QuestionCategory struct {
	ID            uuid.UUID `json:"id"`
	CourseID      uuid.UUID `json:"course_id"`
	Name          string    `json:"name"`
	Position      int       `json:"position"`
	QuestionCount int       `json:"question_count"`
}

// Question is one bank question with its options (including the correct
// flags: this shape is for staff only).
type Question struct {
	ID         uuid.UUID `json:"id"`
	CategoryID uuid.UUID `json:"category_id"`
	Kind       string    `json:"kind"`
	Prompt     string    `json:"prompt"`
	Feedback   string    `json:"feedback"`
	Points     int       `json:"points"`
	Position   int       `json:"position"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Locked is true once the question appears in a submitted attempt:
	// options, regions and kind can't change until the quiz is reset.
	Locked  bool             `json:"locked"`
	Options []QuestionOption `json:"options"`
	// Media is shown above the prompt; FeedbackMedia with the result.
	MediaID         *uuid.UUID `json:"media_id"`
	Media           *MediaRef  `json:"media,omitempty"`
	FeedbackMediaID *uuid.UUID `json:"feedback_media_id"`
	FeedbackMedia   *MediaRef  `json:"feedback_media,omitempty"`
	// Hotspot only: allowed taps (nil = one per correct region) and regions.
	HotspotClicks *int             `json:"hotspot_clicks"`
	Regions       []QuestionRegion `json:"regions"`
}

// QuestionOption is one answer option: a label, a picture, or both.
type QuestionOption struct {
	ID         uuid.UUID  `json:"id"`
	QuestionID uuid.UUID  `json:"question_id"`
	Label      string     `json:"label"`
	Correct    bool       `json:"correct"`
	Position   int        `json:"position"`
	MediaID    *uuid.UUID `json:"media_id"`
	Media      *MediaRef  `json:"media,omitempty"`
}

// OptionInput is one option in a question create payload.
type OptionInput struct {
	Label   string
	Correct bool
	MediaID *uuid.UUID
}

// validateQuestion enforces the per-kind rules on a question's full state:
// choice questions need valid options; hotspot questions need an image and
// at least one correct region (and enough taps to hit them all).
func validateQuestion(q Question) error {
	switch q.Kind {
	case domain.QuestionSingle, domain.QuestionMultiple:
		return validateOptions(q.Kind, q.Options)
	case domain.QuestionHotspot:
		if q.Media == nil || q.Media.Kind != domain.MediaImage {
			return httperror.Validation("a hotspot question needs an image", nil)
		}
		n := correctRegionCount(q.Regions)
		if n == 0 {
			return httperror.Validation("a hotspot question needs at least one correct region", nil)
		}
		if q.HotspotClicks != nil && *q.HotspotClicks < n {
			return httperror.Validation("a hotspot question must allow at least one tap per correct region", nil)
		}
		return nil
	default:
		return httperror.Validation("kind must be single, multiple or hotspot", nil)
	}
}

// validateOptions enforces the per-kind answer rules on a full option set.
// A label may be empty when the option has a picture.
func validateOptions(kind string, opts []QuestionOption) error {
	if kind != domain.QuestionSingle && kind != domain.QuestionMultiple {
		return httperror.Validation("kind must be single or multiple", nil)
	}
	if len(opts) < 2 {
		return httperror.Validation("a question needs at least two options", nil)
	}
	n := 0
	for _, o := range opts {
		if strings.TrimSpace(o.Label) == "" && o.MediaID == nil {
			return httperror.Validation("every option needs a label or a picture", nil)
		}
		if o.Correct {
			n++
		}
	}
	switch kind {
	case domain.QuestionSingle:
		if n != 1 {
			return httperror.Validation("a single-choice question needs exactly one correct option", nil)
		}
	case domain.QuestionMultiple:
		if n < 1 {
			return httperror.Validation("a multiple-choice question needs at least one correct option", nil)
		}
	default:
		return httperror.Validation("kind must be single or multiple", nil)
	}
	return nil
}

// ----------------------------------------------------------------------------
// Categories
// ----------------------------------------------------------------------------

// ListCategories returns a course's bank categories with question counts.
func (s *Service) ListCategories(ctx context.Context, courseID uuid.UUID) ([]QuestionCategory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.course_id, c.name, c.position, count(q.id)
		  FROM question_categories c
		  LEFT JOIN questions q ON q.category_id = c.id
		 WHERE c.course_id = $1
		 GROUP BY c.id
		 ORDER BY c.position, c.created_at`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuestionCategory{}
	for rows.Next() {
		var c QuestionCategory
		if err := rows.Scan(&c.ID, &c.CourseID, &c.Name, &c.Position, &c.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCategory appends a category to the course's bank.
func (s *Service) CreateCategory(ctx context.Context, courseID uuid.UUID, name string) (QuestionCategory, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return QuestionCategory{}, httperror.Validation("name is required", nil)
	}
	c := QuestionCategory{}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO question_categories (course_id, name, position)
		VALUES ($1, $2, (SELECT COALESCE(MAX(position), -1) + 1 FROM question_categories WHERE course_id = $1))
		RETURNING id, course_id, name, position`, courseID, name,
	).Scan(&c.ID, &c.CourseID, &c.Name, &c.Position)
	return c, err
}

// UpdateCategory renames or reorders a category.
func (s *Service) UpdateCategory(ctx context.Context, id uuid.UUID, name *string, position *int) (QuestionCategory, error) {
	if name != nil {
		*name = strings.TrimSpace(*name)
		if *name == "" {
			return QuestionCategory{}, httperror.Validation("name must not be empty", nil)
		}
	}
	c := QuestionCategory{}
	err := s.pool.QueryRow(ctx, `
		UPDATE question_categories
		   SET name = COALESCE($2, name), position = COALESCE($3, position), updated_at = now()
		 WHERE id = $1
		 RETURNING id, course_id, name, position,
		           (SELECT count(*) FROM questions WHERE category_id = $1)`, id, name, position,
	).Scan(&c.ID, &c.CourseID, &c.Name, &c.Position, &c.QuestionCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, httperror.NotFound("category")
	}
	return c, err
}

// DeleteCategory removes a category with its questions and the quiz slots
// that use them. Refused while any of its questions appear in attempts.
func (s *Service) DeleteCategory(ctx context.Context, id uuid.UUID) error {
	var used bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM quiz_attempt_questions aq
			  JOIN questions q ON q.id = aq.question_id
			 WHERE q.category_id = $1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return httperror.Conflict("questions in this category appear in quiz attempts; reset those quizzes first")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM question_categories WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("category")
	}
	return err
}

// CategoryCourse resolves a category's course for authorization.
func (s *Service) CategoryCourse(ctx context.Context, categoryID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c JOIN question_categories qc ON qc.course_id = c.id
		 WHERE qc.id = $1`, categoryID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("category")
	}
	return c, err
}

// ----------------------------------------------------------------------------
// Questions
// ----------------------------------------------------------------------------

const questionCols = `q.id, q.category_id, q.kind, q.prompt, q.feedback, q.points, q.position, q.created_at, q.updated_at,
	EXISTS (SELECT 1 FROM quiz_attempt_questions aq JOIN quiz_attempts a ON a.id = aq.attempt_id
	         WHERE aq.question_id = q.id AND a.submitted_at IS NOT NULL),
	q.media_id, q.feedback_media_id, q.hotspot_clicks`

func scanQuestion(row pgx.Row) (Question, error) {
	q := Question{Options: []QuestionOption{}, Regions: []QuestionRegion{}}
	err := row.Scan(&q.ID, &q.CategoryID, &q.Kind, &q.Prompt, &q.Feedback, &q.Points, &q.Position,
		&q.CreatedAt, &q.UpdatedAt, &q.Locked, &q.MediaID, &q.FeedbackMediaID, &q.HotspotClicks)
	return q, err
}

// ListQuestions returns a category's questions with their options.
func (s *Service) ListQuestions(ctx context.Context, categoryID uuid.UUID) ([]Question, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+questionCols+`
		  FROM questions q
		 WHERE q.category_id = $1
		 ORDER BY q.position, q.created_at`, categoryID)
	if err != nil {
		return nil, err
	}
	out := []Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachOptions(ctx, out)
}

// GetQuestion loads one question with options.
func (s *Service) GetQuestion(ctx context.Context, id uuid.UUID) (Question, error) {
	q, err := scanQuestion(s.pool.QueryRow(ctx, `SELECT `+questionCols+` FROM questions q WHERE q.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return q, httperror.NotFound("question")
	}
	if err != nil {
		return q, err
	}
	qs := []Question{q}
	if err := s.attachOptions(ctx, qs); err != nil {
		return q, err
	}
	return qs[0], nil
}

func (s *Service) attachOptions(ctx context.Context, qs []Question) error {
	if len(qs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(qs))
	index := map[uuid.UUID]int{}
	for i, q := range qs {
		ids[i] = q.ID
		index[q.ID] = i
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, question_id, label, correct, position, media_id
		  FROM question_options
		 WHERE question_id = ANY($1)
		 ORDER BY position, id`, ids)
	if err != nil {
		return err
	}
	mediaIDs := []uuid.UUID{}
	for rows.Next() {
		var o QuestionOption
		if err := rows.Scan(&o.ID, &o.QuestionID, &o.Label, &o.Correct, &o.Position, &o.MediaID); err != nil {
			rows.Close()
			return err
		}
		if o.MediaID != nil {
			mediaIDs = append(mediaIDs, *o.MediaID)
		}
		i := index[o.QuestionID]
		qs[i].Options = append(qs[i].Options, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return s.attachQuestionExtras(ctx, qs, mediaIDs)
}

// attachQuestionExtras fills hotspot regions and the media refs of
// questions and their options.
func (s *Service) attachQuestionExtras(ctx context.Context, qs []Question, mediaIDs []uuid.UUID) error {
	ids := make([]uuid.UUID, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
		for _, m := range []*uuid.UUID{q.MediaID, q.FeedbackMediaID} {
			if m != nil {
				mediaIDs = append(mediaIDs, *m)
			}
		}
	}
	regions, err := s.regionsFor(ctx, s.pool, ids)
	if err != nil {
		return err
	}
	refs, err := s.mediaRefs(ctx, mediaIDs)
	if err != nil {
		return err
	}
	for i := range qs {
		qs[i].Regions = regions[qs[i].ID]
		if qs[i].Regions == nil {
			qs[i].Regions = []QuestionRegion{}
		}
		qs[i].Media = refFor(refs, qs[i].MediaID)
		qs[i].FeedbackMedia = refFor(refs, qs[i].FeedbackMediaID)
		for j := range qs[i].Options {
			qs[i].Options[j].Media = refFor(refs, qs[i].Options[j].MediaID)
		}
	}
	return nil
}

// CreateQuestionInput is the create payload. A question is created with
// its full option set so it is valid from the start.
type CreateQuestionInput struct {
	Kind            string
	Prompt          string
	Feedback        string
	Points          *int
	Options         []OptionInput
	MediaID         *uuid.UUID
	FeedbackMediaID *uuid.UUID
	HotspotClicks   *int
	Regions         []RegionInput
}

// CreateQuestion appends a question (with its options, or its regions for
// a hotspot question) to a category.
func (s *Service) CreateQuestion(ctx context.Context, categoryID uuid.UUID, in CreateQuestionInput) (Question, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Prompt == "" {
		return Question{}, httperror.Validation("prompt is required", nil)
	}
	if in.Kind == "" {
		in.Kind = domain.QuestionSingle
	}
	points := 1
	if in.Points != nil {
		points = *in.Points
	}
	if points <= 0 {
		return Question{}, httperror.Validation("points must be positive", nil)
	}
	course, err := s.CategoryCourse(ctx, categoryID)
	if err != nil {
		return Question{}, err
	}
	draft := Question{Kind: in.Kind, HotspotClicks: in.HotspotClicks, MediaID: in.MediaID}
	if in.MediaID != nil {
		ref, err := attachableMedia(ctx, s.pool, course.ID, *in.MediaID)
		if err != nil {
			return Question{}, err
		}
		draft.Media = &ref
	}
	if in.FeedbackMediaID != nil {
		if _, err := attachableMedia(ctx, s.pool, course.ID, *in.FeedbackMediaID); err != nil {
			return Question{}, err
		}
	}
	var regions []RegionInput
	if in.Kind == domain.QuestionHotspot {
		if len(in.Options) > 0 {
			return Question{}, httperror.Validation("hotspot questions have regions, not options", nil)
		}
		if regions, err = normalizeRegions(in.Regions); err != nil {
			return Question{}, err
		}
		draft.Regions = regionsFromInput(regions)
	} else {
		if len(in.Regions) > 0 {
			return Question{}, httperror.Validation("only hotspot questions have regions", nil)
		}
		if in.HotspotClicks != nil {
			return Question{}, httperror.Validation("hotspot_clicks only applies to hotspot questions", nil)
		}
		for i := range in.Options {
			in.Options[i].Label = strings.TrimSpace(in.Options[i].Label)
			if m := in.Options[i].MediaID; m != nil {
				if _, err := attachableMedia(ctx, s.pool, course.ID, *m); err != nil {
					return Question{}, err
				}
			}
			draft.Options = append(draft.Options, QuestionOption{
				Label: in.Options[i].Label, Correct: in.Options[i].Correct, MediaID: in.Options[i].MediaID,
			})
		}
	}
	if err := validateQuestion(draft); err != nil {
		return Question{}, err
	}
	var id uuid.UUID
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO questions (category_id, kind, prompt, feedback, points, position, media_id, feedback_media_id, hotspot_clicks)
			VALUES ($1, $2, $3, $4, $5, (SELECT COALESCE(MAX(position), -1) + 1 FROM questions WHERE category_id = $1), $6, $7, $8)
			RETURNING id`,
			categoryID, in.Kind, in.Prompt, strings.TrimSpace(in.Feedback), points,
			in.MediaID, in.FeedbackMediaID, in.HotspotClicks,
		).Scan(&id); err != nil {
			return err
		}
		for i, o := range draft.Options {
			if _, err := tx.Exec(ctx, `
				INSERT INTO question_options (question_id, label, correct, position, media_id) VALUES ($1, $2, $3, $4, $5)`,
				id, o.Label, o.Correct, i, o.MediaID); err != nil {
				return err
			}
		}
		return insertRegions(ctx, tx, id, regions)
	})
	if err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, id)
}

// UpdateQuestionInput carries optional question fields; nil = unchanged.
type UpdateQuestionInput struct {
	Kind     *string
	Prompt   *string
	Feedback *string
	Points   *int
	Position *int
	// Media fields: a value sets it, Clear* removes it.
	MediaID            *uuid.UUID
	ClearMedia         bool
	FeedbackMediaID    *uuid.UUID
	ClearFeedbackMedia bool
	HotspotClicks      *int
	ClearHotspotClicks bool
}

// UpdateQuestion edits a question. Prompt, feedback and points stay
// editable after use; changing the kind (or a hotspot's image or tap
// count) is refused once locked, and the result must stay valid.
func (s *Service) UpdateQuestion(ctx context.Context, id uuid.UUID, in UpdateQuestionInput) (Question, error) {
	if in.Prompt != nil {
		*in.Prompt = strings.TrimSpace(*in.Prompt)
		if *in.Prompt == "" {
			return Question{}, httperror.Validation("prompt must not be empty", nil)
		}
	}
	if in.Points != nil && *in.Points <= 0 {
		return Question{}, httperror.Validation("points must be positive", nil)
	}
	cur, err := s.GetQuestion(ctx, id)
	if err != nil {
		return Question{}, err
	}
	next := cur
	shapeChanged := false
	if in.Kind != nil && *in.Kind != cur.Kind {
		if cur.Locked {
			return Question{}, httperror.Conflict("this question has been answered in a submitted attempt; reset the quiz to change its kind")
		}
		next.Kind = *in.Kind
		shapeChanged = true
	}
	mediaChanged := in.ClearMedia || (in.MediaID != nil && (cur.MediaID == nil || *cur.MediaID != *in.MediaID))
	clicksChanged := in.ClearHotspotClicks || in.HotspotClicks != nil
	if (mediaChanged || clicksChanged) && cur.Locked && cur.Kind == domain.QuestionHotspot {
		return Question{}, httperror.Conflict("this question has been answered in a submitted attempt; reset the quiz to change its image or taps")
	}
	if in.MediaID != nil || in.FeedbackMediaID != nil {
		course, err := s.QuestionCourse(ctx, id)
		if err != nil {
			return Question{}, err
		}
		if in.MediaID != nil {
			ref, err := attachableMedia(ctx, s.pool, course.ID, *in.MediaID)
			if err != nil {
				return Question{}, err
			}
			next.MediaID, next.Media = in.MediaID, &ref
		}
		if in.FeedbackMediaID != nil {
			if _, err := attachableMedia(ctx, s.pool, course.ID, *in.FeedbackMediaID); err != nil {
				return Question{}, err
			}
		}
	}
	if in.ClearMedia {
		next.MediaID, next.Media = nil, nil
	}
	if in.HotspotClicks != nil {
		next.HotspotClicks = in.HotspotClicks
	}
	if in.ClearHotspotClicks {
		next.HotspotClicks = nil
	}
	if next.HotspotClicks != nil && next.Kind != domain.QuestionHotspot {
		if in.HotspotClicks != nil {
			return Question{}, httperror.Validation("hotspot_clicks only applies to hotspot questions", nil)
		}
		next.HotspotClicks = nil
		in.ClearHotspotClicks = true
	}
	if shapeChanged || mediaChanged || clicksChanged {
		if err := validateQuestion(next); err != nil {
			return Question{}, err
		}
	}
	if in.Feedback != nil {
		*in.Feedback = strings.TrimSpace(*in.Feedback)
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE questions
		   SET kind = COALESCE($2, kind), prompt = COALESCE($3, prompt), feedback = COALESCE($4, feedback),
		       points = COALESCE($5, points), position = COALESCE($6, position),
		       media_id = CASE WHEN $8 THEN NULL ELSE COALESCE($7, media_id) END,
		       feedback_media_id = CASE WHEN $10 THEN NULL ELSE COALESCE($9, feedback_media_id) END,
		       hotspot_clicks = CASE WHEN $12 THEN NULL ELSE COALESCE($11, hotspot_clicks) END,
		       updated_at = now()
		 WHERE id = $1`, id, in.Kind, in.Prompt, in.Feedback, in.Points, in.Position,
		in.MediaID, in.ClearMedia, in.FeedbackMediaID, in.ClearFeedbackMedia,
		in.HotspotClicks, in.ClearHotspotClicks); err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, id)
}

// DeleteQuestion removes a question. Refused while it appears in attempts.
func (s *Service) DeleteQuestion(ctx context.Context, id uuid.UUID) error {
	var used bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM quiz_attempt_questions WHERE question_id = $1)`, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return httperror.Conflict("this question appears in quiz attempts; reset those quizzes first")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM questions WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("question")
	}
	return err
}

// QuestionCourse resolves a question's course for authorization.
func (s *Service) QuestionCourse(ctx context.Context, questionID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN question_categories qc ON qc.course_id = c.id
		  JOIN questions q ON q.category_id = qc.id
		 WHERE q.id = $1`, questionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("question")
	}
	return c, err
}

// ----------------------------------------------------------------------------
// Options
// ----------------------------------------------------------------------------

// lockedQuestion loads a question for an option change, refusing locked
// ones and hotspot questions (which have regions instead).
func (s *Service) lockedQuestion(ctx context.Context, questionID uuid.UUID) (Question, error) {
	q, err := s.GetQuestion(ctx, questionID)
	if err != nil {
		return q, err
	}
	if q.Kind == domain.QuestionHotspot {
		return q, httperror.Validation("hotspot questions have regions, not options", nil)
	}
	if q.Locked {
		return q, httperror.Conflict("this question has been answered in a submitted attempt; reset the quiz to change its options")
	}
	return q, nil
}

// optionMedia checks a picture for an option of questionID's course.
func (s *Service) optionMedia(ctx context.Context, questionID, mediaID uuid.UUID) error {
	course, err := s.QuestionCourse(ctx, questionID)
	if err != nil {
		return err
	}
	_, err = attachableMedia(ctx, s.pool, course.ID, mediaID)
	return err
}

// CreateOption adds an option (a label, a picture, or both). On a
// single-choice question a new correct option takes over from the previous one.
func (s *Service) CreateOption(ctx context.Context, questionID uuid.UUID, in OptionInput) (Question, error) {
	label := strings.TrimSpace(in.Label)
	correct := in.Correct
	if label == "" && in.MediaID == nil {
		return Question{}, httperror.Validation("an option needs a label or a picture", nil)
	}
	q, err := s.lockedQuestion(ctx, questionID)
	if err != nil {
		return Question{}, err
	}
	if in.MediaID != nil {
		if err := s.optionMedia(ctx, questionID, *in.MediaID); err != nil {
			return Question{}, err
		}
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if correct && q.Kind == domain.QuestionSingle {
			if _, err := tx.Exec(ctx, `UPDATE question_options SET correct = FALSE WHERE question_id = $1`, questionID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO question_options (question_id, label, correct, position, media_id)
			VALUES ($1, $2, $3, (SELECT COALESCE(MAX(position), -1) + 1 FROM question_options WHERE question_id = $1), $4)`,
			questionID, label, correct, in.MediaID)
		return err
	})
	if err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, questionID)
}

// UpdateOptionInput carries optional option fields; nil = unchanged.
type UpdateOptionInput struct {
	Label      *string
	Correct    *bool
	Position   *int
	MediaID    *uuid.UUID
	ClearMedia bool
}

// UpdateOption edits an option. Marking an option correct on a
// single-choice question unmarks the others; the result must stay valid.
func (s *Service) UpdateOption(ctx context.Context, optionID uuid.UUID, in UpdateOptionInput) (Question, error) {
	questionID, err := s.optionQuestion(ctx, optionID)
	if err != nil {
		return Question{}, err
	}
	q, err := s.lockedQuestion(ctx, questionID)
	if err != nil {
		return Question{}, err
	}
	if in.Label != nil {
		*in.Label = strings.TrimSpace(*in.Label)
	}
	if in.MediaID != nil {
		if err := s.optionMedia(ctx, questionID, *in.MediaID); err != nil {
			return Question{}, err
		}
	}
	next := make([]QuestionOption, len(q.Options))
	copy(next, q.Options)
	for i, o := range next {
		if o.ID == optionID {
			if in.Label != nil {
				next[i].Label = *in.Label
			}
			if in.Correct != nil {
				next[i].Correct = *in.Correct
			}
			if in.MediaID != nil {
				next[i].MediaID = in.MediaID
			}
			if in.ClearMedia {
				next[i].MediaID = nil
			}
		} else if in.Correct != nil && *in.Correct && q.Kind == domain.QuestionSingle {
			next[i].Correct = false
		}
	}
	if err := validateOptions(q.Kind, next); err != nil {
		return Question{}, err
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if in.Correct != nil && *in.Correct && q.Kind == domain.QuestionSingle {
			if _, err := tx.Exec(ctx, `UPDATE question_options SET correct = FALSE WHERE question_id = $1 AND id <> $2`,
				questionID, optionID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `
			UPDATE question_options
			   SET label = COALESCE($2, label), correct = COALESCE($3, correct), position = COALESCE($4, position),
			       media_id = CASE WHEN $6 THEN NULL ELSE COALESCE($5, media_id) END
			 WHERE id = $1`, optionID, in.Label, in.Correct, in.Position, in.MediaID, in.ClearMedia)
		return err
	})
	if err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, questionID)
}

// DeleteOption removes an option, keeping the question valid.
func (s *Service) DeleteOption(ctx context.Context, optionID uuid.UUID) (Question, error) {
	questionID, err := s.optionQuestion(ctx, optionID)
	if err != nil {
		return Question{}, err
	}
	q, err := s.lockedQuestion(ctx, questionID)
	if err != nil {
		return Question{}, err
	}
	remaining := []QuestionOption{}
	for _, o := range q.Options {
		if o.ID != optionID {
			remaining = append(remaining, o)
		}
	}
	if err := validateOptions(q.Kind, remaining); err != nil {
		return Question{}, err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM question_options WHERE id = $1`, optionID); err != nil {
		return Question{}, err
	}
	return s.GetQuestion(ctx, questionID)
}

func (s *Service) optionQuestion(ctx context.Context, optionID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT question_id FROM question_options WHERE id = $1`, optionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return id, httperror.NotFound("option")
	}
	return id, err
}

// OptionCourse resolves an option's course for authorization.
func (s *Service) OptionCourse(ctx context.Context, optionID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN question_categories qc ON qc.course_id = c.id
		  JOIN questions q ON q.category_id = qc.id
		  JOIN question_options o ON o.question_id = q.id
		 WHERE o.id = $1`, optionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("option")
	}
	return c, err
}
