package learn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// QuizStanding is one person's standing on one quiz. The grade is the best
// submitted attempt.
type QuizStanding struct {
	AttemptsUsed int `json:"attempts_used"`
	// AttemptsLeft is nil when attempts are unlimited.
	AttemptsLeft  *int       `json:"attempts_left"`
	BestScore     *int       `json:"best_score"`
	BestMaxScore  *int       `json:"best_max_score"`
	BestPercent   *int       `json:"best_percent"`
	Passed        bool       `json:"passed"`
	OpenAttemptID *uuid.UUID `json:"open_attempt_id"`
}

// QuizSlot is one entry of a quiz: a fixed question, or a random draw of
// DrawCount questions from a category.
type QuizSlot struct {
	ID             uuid.UUID  `json:"id"`
	ActivityID     uuid.UUID  `json:"activity_id"`
	Position       int        `json:"position"`
	QuestionID     *uuid.UUID `json:"question_id"`
	CategoryID     *uuid.UUID `json:"category_id"`
	DrawCount      *int       `json:"draw_count"`
	QuestionPrompt string     `json:"question_prompt,omitempty"`
	CategoryName   string     `json:"category_name,omitempty"`
}

// AttemptSummary is an attempt without its questions.
type AttemptSummary struct {
	ID          uuid.UUID  `json:"id"`
	ActivityID  uuid.UUID  `json:"activity_id"`
	UserID      uuid.UUID  `json:"user_id"`
	StartedAt   time.Time  `json:"started_at"`
	SubmittedAt *time.Time `json:"submitted_at"`
	Score       *int       `json:"score"`
	MaxScore    *int       `json:"max_score"`
	Percent     *int       `json:"percent"`
	Passed      *bool      `json:"passed"`
}

// Attempt is an attempt with its frozen questions. Correct answers and
// feedback are only filled in once the attempt is submitted.
type Attempt struct {
	AttemptSummary
	PassPercent int               `json:"pass_percent"`
	Questions   []AttemptQuestion `json:"questions"`
}

// AttemptQuestion is one question as the learner sees it.
type AttemptQuestion struct {
	QuestionID      uuid.UUID       `json:"question_id"`
	Position        int             `json:"position"`
	Kind            string          `json:"kind"`
	Prompt          string          `json:"prompt"`
	Points          int             `json:"points"`
	Options         []AttemptOption `json:"options"`
	ChosenOptionIDs []uuid.UUID     `json:"chosen_option_ids"`

	Correct          *bool       `json:"correct,omitempty"`
	PointsAwarded    *int        `json:"points_awarded,omitempty"`
	CorrectOptionIDs []uuid.UUID `json:"correct_option_ids,omitempty"`
	Feedback         *string     `json:"feedback,omitempty"`

	Media *MediaRef `json:"media,omitempty"`
	// Hotspot: the tap limit and the learner's taps. Regions are only sent
	// once the attempt is submitted.
	MaxTaps       int              `json:"max_taps,omitempty"`
	Taps          []Point          `json:"taps,omitempty"`
	Regions       []QuestionRegion `json:"regions,omitempty"`
	FeedbackMedia *MediaRef        `json:"feedback_media,omitempty"`
}

// AttemptOption is an option without its correct flag.
type AttemptOption struct {
	ID    uuid.UUID `json:"id"`
	Label string    `json:"label"`
	Media *MediaRef `json:"media,omitempty"`
}

// QuizInfo is the learner's view of a quiz before (or between) attempts.
type QuizInfo struct {
	ActivityID     uuid.UUID        `json:"activity_id"`
	Title          string           `json:"title"`
	Body           string           `json:"body"`
	PassPercent    int              `json:"pass_percent"`
	MaxAttempts    *int             `json:"max_attempts"`
	ShuffleOptions bool             `json:"shuffle_options"`
	QuestionCount  int              `json:"question_count"`
	Standing       QuizStanding     `json:"standing"`
	Attempts       []AttemptSummary `json:"attempts"`
}

func validateQuizSettings(passPercent, maxAttempts *int) error {
	if passPercent != nil && (*passPercent < 0 || *passPercent > 100) {
		return httperror.Validation("pass_percent must be between 0 and 100", nil)
	}
	if maxAttempts != nil && *maxAttempts <= 0 {
		return httperror.Validation("max_attempts must be positive (omit it for unlimited)", nil)
	}
	return nil
}

func percentOf(score, maxScore *int) *int {
	if score == nil || maxScore == nil || *maxScore == 0 {
		return nil
	}
	p := *score * 100 / *maxScore
	return &p
}

// QuizActivity loads an activity and checks it is a quiz.
func (s *Service) QuizActivity(ctx context.Context, id uuid.UUID) (Activity, error) {
	a, err := scanActivity(s.pool.QueryRow(ctx, `SELECT `+activityCols+` FROM activities WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && a.Kind != domain.ActivityQuiz) {
		return Activity{}, httperror.NotFound("quiz")
	}
	return a, err
}

// ----------------------------------------------------------------------------
// Standings
// ----------------------------------------------------------------------------

// quizStandings returns userID's standing on every quiz matched by filter
// ("a.id = $1" or "s.course_id = $1"), keyed by activity id.
func (s *Service) quizStandings(ctx context.Context, filter string, arg, userID uuid.UUID) (map[uuid.UUID]QuizStanding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.max_attempts,
		       count(qa.id) FILTER (WHERE qa.submitted_at IS NOT NULL),
		       COALESCE(bool_or(qa.passed), FALSE),
		       (array_agg(qa.id) FILTER (WHERE qa.submitted_at IS NULL))[1],
		       best.score, best.max_score
		  FROM activities a
		  JOIN sections s ON s.id = a.section_id
		  LEFT JOIN quiz_attempts qa ON qa.activity_id = a.id AND qa.user_id = $2 AND qa.cycle = `+cycleOf("s.course_id", "$2")+`
		  LEFT JOIN LATERAL (
		        SELECT b.score, b.max_score
		          FROM quiz_attempts b
		         WHERE b.activity_id = a.id AND b.user_id = $2 AND b.submitted_at IS NOT NULL
		           AND b.cycle = `+cycleOf("s.course_id", "$2")+`
		         ORDER BY b.score::float / NULLIF(b.max_score, 0) DESC NULLS LAST, b.submitted_at
		         LIMIT 1) best ON TRUE
		 WHERE a.kind = 'quiz' AND `+filter+`
		 GROUP BY a.id, best.score, best.max_score`, arg, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]QuizStanding{}
	for rows.Next() {
		var id uuid.UUID
		var maxAttempts *int
		var st QuizStanding
		if err := rows.Scan(&id, &maxAttempts, &st.AttemptsUsed, &st.Passed, &st.OpenAttemptID,
			&st.BestScore, &st.BestMaxScore); err != nil {
			return nil, err
		}
		st.BestPercent = percentOf(st.BestScore, st.BestMaxScore)
		if maxAttempts != nil {
			left := max(*maxAttempts-st.AttemptsUsed, 0)
			st.AttemptsLeft = &left
		}
		out[id] = st
	}
	return out, rows.Err()
}

// attachQuizStandings fills Activity.Quiz for every quiz in a course detail.
func (s *Service) attachQuizStandings(ctx context.Context, d *CourseDetail, userID uuid.UUID) error {
	standings, err := s.quizStandings(ctx, "s.course_id = $1", d.ID, userID)
	if err != nil {
		return err
	}
	for i := range d.Sections {
		for j := range d.Sections[i].Activities {
			a := &d.Sections[i].Activities[j]
			if st, ok := standings[a.ID]; ok {
				a.Quiz = &st
			}
		}
	}
	return nil
}

// QuizInfo assembles the learner's view of a quiz: settings, standing and
// their own attempts (no questions, no answers).
func (s *Service) QuizInfo(ctx context.Context, activityID, userID uuid.UUID) (QuizInfo, error) {
	a, err := s.QuizActivity(ctx, activityID)
	if err != nil {
		return QuizInfo{}, err
	}
	info := QuizInfo{
		ActivityID: a.ID, Title: a.Title, Body: a.Body, PassPercent: a.PassPercent,
		MaxAttempts: a.MaxAttempts, ShuffleOptions: a.ShuffleOptions, Attempts: []AttemptSummary{},
	}
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(draw_count, 1)), 0) FROM quiz_slots WHERE activity_id = $1`, activityID,
	).Scan(&info.QuestionCount); err != nil {
		return info, err
	}
	standings, err := s.quizStandings(ctx, "a.id = $1", activityID, userID)
	if err != nil {
		return info, err
	}
	info.Standing = standings[activityID]
	rows, err := s.pool.Query(ctx, `
		SELECT `+attemptCols+` FROM quiz_attempts
		 WHERE activity_id = $1 AND user_id = $2 AND cycle = `+activityCycleOf("$1", "$2")+`
		 ORDER BY started_at`, activityID, userID)
	if err != nil {
		return info, err
	}
	defer rows.Close()
	for rows.Next() {
		as, err := scanAttemptSummary(rows)
		if err != nil {
			return info, err
		}
		info.Attempts = append(info.Attempts, as)
	}
	return info, rows.Err()
}

// ----------------------------------------------------------------------------
// Slots
// ----------------------------------------------------------------------------

// ListSlots returns a quiz's slots in order.
func (s *Service) ListSlots(ctx context.Context, activityID uuid.UUID) ([]QuizSlot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sl.id, sl.activity_id, sl.position, sl.question_id, sl.category_id, sl.draw_count,
		       COALESCE(q.prompt, ''), COALESCE(c.name, '')
		  FROM quiz_slots sl
		  LEFT JOIN questions q ON q.id = sl.question_id
		  LEFT JOIN question_categories c ON c.id = sl.category_id
		 WHERE sl.activity_id = $1
		 ORDER BY sl.position, sl.id`, activityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuizSlot{}
	for rows.Next() {
		var sl QuizSlot
		if err := rows.Scan(&sl.ID, &sl.ActivityID, &sl.Position, &sl.QuestionID, &sl.CategoryID, &sl.DrawCount,
			&sl.QuestionPrompt, &sl.CategoryName); err != nil {
			return nil, err
		}
		out = append(out, sl)
	}
	return out, rows.Err()
}

// CreateSlotInput is either QuestionID, or CategoryID + DrawCount.
type CreateSlotInput struct {
	QuestionID *uuid.UUID
	CategoryID *uuid.UUID
	DrawCount  *int
}

// CreateSlot appends a slot to a quiz. The question or category must belong
// to the quiz's own course.
func (s *Service) CreateSlot(ctx context.Context, activityID uuid.UUID, in CreateSlotInput) (QuizSlot, error) {
	if _, err := s.QuizActivity(ctx, activityID); err != nil {
		return QuizSlot{}, err
	}
	var sameCourse bool
	var err error
	switch {
	case in.QuestionID != nil && in.CategoryID == nil && in.DrawCount == nil:
		err = s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM questions q
				  JOIN question_categories qc ON qc.id = q.category_id
				  JOIN sections s ON s.course_id = qc.course_id
				  JOIN activities a ON a.section_id = s.id
				 WHERE q.id = $1 AND a.id = $2)`, *in.QuestionID, activityID).Scan(&sameCourse)
	case in.QuestionID == nil && in.CategoryID != nil:
		if in.DrawCount == nil || *in.DrawCount <= 0 {
			return QuizSlot{}, httperror.Validation("draw_count must be positive", nil)
		}
		err = s.pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM question_categories qc
				  JOIN sections s ON s.course_id = qc.course_id
				  JOIN activities a ON a.section_id = s.id
				 WHERE qc.id = $1 AND a.id = $2)`, *in.CategoryID, activityID).Scan(&sameCourse)
	default:
		return QuizSlot{}, httperror.Validation("give either question_id, or category_id with draw_count", nil)
	}
	if err != nil {
		return QuizSlot{}, err
	}
	if !sameCourse {
		return QuizSlot{}, httperror.Validation("the question or category must belong to this course", nil)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO quiz_slots (activity_id, position, question_id, category_id, draw_count)
		VALUES ($1, (SELECT COALESCE(MAX(position), -1) + 1 FROM quiz_slots WHERE activity_id = $1), $2, $3, $4)
		RETURNING id`, activityID, in.QuestionID, in.CategoryID, in.DrawCount).Scan(&id); err != nil {
		return QuizSlot{}, err
	}
	return s.getSlot(ctx, id)
}

func (s *Service) getSlot(ctx context.Context, id uuid.UUID) (QuizSlot, error) {
	var activityID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT activity_id FROM quiz_slots WHERE id = $1`, id).Scan(&activityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return QuizSlot{}, httperror.NotFound("slot")
	}
	if err != nil {
		return QuizSlot{}, err
	}
	slots, err := s.ListSlots(ctx, activityID)
	if err != nil {
		return QuizSlot{}, err
	}
	for _, sl := range slots {
		if sl.ID == id {
			return sl, nil
		}
	}
	return QuizSlot{}, httperror.NotFound("slot")
}

// UpdateSlot reorders a slot or changes a category draw's size.
func (s *Service) UpdateSlot(ctx context.Context, id uuid.UUID, position, drawCount *int) (QuizSlot, error) {
	cur, err := s.getSlot(ctx, id)
	if err != nil {
		return QuizSlot{}, err
	}
	if drawCount != nil {
		if cur.CategoryID == nil {
			return QuizSlot{}, httperror.Validation("draw_count only applies to category slots", nil)
		}
		if *drawCount <= 0 {
			return QuizSlot{}, httperror.Validation("draw_count must be positive", nil)
		}
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE quiz_slots SET position = COALESCE($2, position), draw_count = COALESCE($3, draw_count)
		 WHERE id = $1`, id, position, drawCount); err != nil {
		return QuizSlot{}, err
	}
	return s.getSlot(ctx, id)
}

// DeleteSlot removes a slot. Attempts already started keep their questions.
func (s *Service) DeleteSlot(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM quiz_slots WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("slot")
	}
	return err
}

// SlotCourse resolves a slot's course for authorization.
func (s *Service) SlotCourse(ctx context.Context, slotID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id
		  JOIN quiz_slots sl ON sl.activity_id = a.id
		 WHERE sl.id = $1`, slotID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("slot")
	}
	return c, err
}

// ----------------------------------------------------------------------------
// Validation
// ----------------------------------------------------------------------------

// QuizProblems lists what stops a quiz from being taken: no slots, a
// question used twice, a category draw larger than the questions left in
// it, or a question whose options break the rules. Empty = ready.
func (s *Service) QuizProblems(ctx context.Context, activityID uuid.UUID) ([]string, error) {
	slots, err := s.ListSlots(ctx, activityID)
	if err != nil {
		return nil, err
	}
	problems := []string{}
	if len(slots) == 0 {
		return append(problems, "the quiz has no questions"), nil
	}
	fixed := map[uuid.UUID]bool{}
	draws := map[uuid.UUID]int{}
	names := map[uuid.UUID]string{}
	categoryIDs := []uuid.UUID{}
	for _, sl := range slots {
		switch {
		case sl.QuestionID != nil:
			if fixed[*sl.QuestionID] {
				problems = append(problems, fmt.Sprintf("question %q is used more than once", sl.QuestionPrompt))
			}
			fixed[*sl.QuestionID] = true
		case sl.CategoryID != nil:
			if _, ok := draws[*sl.CategoryID]; !ok {
				categoryIDs = append(categoryIDs, *sl.CategoryID)
			}
			draws[*sl.CategoryID] += *sl.DrawCount
			names[*sl.CategoryID] = sl.CategoryName
		}
	}
	fixedIDs := make([]uuid.UUID, 0, len(fixed))
	for id := range fixed {
		fixedIDs = append(fixedIDs, id)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT `+questionCols+`
		  FROM questions q
		 WHERE q.id = ANY($1) OR q.category_id = ANY($2)
		 ORDER BY q.position, q.created_at`, fixedIDs, categoryIDs)
	if err != nil {
		return nil, err
	}
	qs := []Question{}
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		qs = append(qs, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachOptions(ctx, qs); err != nil {
		return nil, err
	}

	available := map[uuid.UUID]int{}
	for _, q := range qs {
		if _, drawn := draws[q.CategoryID]; drawn && !fixed[q.ID] {
			available[q.CategoryID]++
		}
		if err := validateQuestion(q); err != nil {
			var he *httperror.Error
			if errors.As(err, &he) {
				problems = append(problems, fmt.Sprintf("question %q: %s", q.Prompt, he.Message))
			}
		}
	}
	for _, id := range categoryIDs {
		if available[id] < draws[id] {
			problems = append(problems, fmt.Sprintf("category %q has %d questions to draw from but the quiz draws %d",
				names[id], available[id], draws[id]))
		}
	}
	return problems, nil
}

// QuizProblemReport names one quiz that is not ready.
type QuizProblemReport struct {
	ActivityID uuid.UUID `json:"activity_id"`
	Title      string    `json:"title"`
	Problems   []string  `json:"problems"`
}

// validateCourseQuizzes returns a 422 listing every quiz in the course that
// isn't ready, or nil.
func (s *Service) validateCourseQuizzes(ctx context.Context, courseID uuid.UUID) error {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title
		  FROM activities a JOIN sections s ON s.id = a.section_id
		 WHERE s.course_id = $1 AND a.kind = 'quiz'
		 ORDER BY s.position, a.position`, courseID)
	if err != nil {
		return err
	}
	quizzes := []QuizProblemReport{}
	for rows.Next() {
		var q QuizProblemReport
		if err := rows.Scan(&q.ActivityID, &q.Title); err != nil {
			rows.Close()
			return err
		}
		quizzes = append(quizzes, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	bad := []QuizProblemReport{}
	for _, q := range quizzes {
		problems, err := s.QuizProblems(ctx, q.ActivityID)
		if err != nil {
			return err
		}
		if len(problems) > 0 {
			q.Problems = problems
			bad = append(bad, q)
		}
	}
	if len(bad) > 0 {
		return httperror.Validation(fmt.Sprintf("quiz %q is not ready: %s", bad[0].Title, bad[0].Problems[0]),
			map[string]any{"quizzes": bad})
	}
	return nil
}

// ----------------------------------------------------------------------------
// Attempts
// ----------------------------------------------------------------------------

const attemptCols = `id, activity_id, user_id, started_at, submitted_at, score, max_score, passed`

func scanAttemptSummary(row pgx.Row) (AttemptSummary, error) {
	var a AttemptSummary
	err := row.Scan(&a.ID, &a.ActivityID, &a.UserID, &a.StartedAt, &a.SubmittedAt, &a.Score, &a.MaxScore, &a.Passed)
	a.Percent = percentOf(a.Score, a.MaxScore)
	return a, err
}

// StartAttempt returns the caller's open attempt, or starts a new one:
// checks max_attempts, draws the questions and freezes the option order.
func (s *Service) StartAttempt(ctx context.Context, activityID, userID uuid.UUID) (Attempt, bool, error) {
	quiz, err := s.QuizActivity(ctx, activityID)
	if err != nil {
		return Attempt{}, false, err
	}
	var attemptID uuid.UUID
	created := false
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"quiz-attempt:"+activityID.String()+":"+userID.String()); err != nil {
			return err
		}
		cycle, err := activityCycle(ctx, tx, activityID, userID)
		if err != nil {
			return err
		}
		var used int
		var open *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE submitted_at IS NOT NULL AND cycle = $3),
			       (array_agg(id) FILTER (WHERE submitted_at IS NULL))[1]
			  FROM quiz_attempts WHERE activity_id = $1 AND user_id = $2`,
			activityID, userID, cycle).Scan(&used, &open); err != nil {
			return err
		}
		if open != nil {
			// An attempt left open before a renewal carries on in the new cycle.
			attemptID = *open
			_, err := tx.Exec(ctx, `UPDATE quiz_attempts SET cycle = $2 WHERE id = $1 AND cycle <> $2`, attemptID, cycle)
			return err
		}
		if quiz.MaxAttempts != nil && used >= *quiz.MaxAttempts {
			return httperror.Conflict("you have used all your attempts on this quiz")
		}
		questionIDs, err := drawQuestions(ctx, tx, activityID)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO quiz_attempts (activity_id, user_id, cycle) VALUES ($1, $2, $3) RETURNING id`,
			activityID, userID, cycle).Scan(&attemptID); err != nil {
			return err
		}
		for i, qid := range questionIDs {
			order, err := optionIDs(ctx, tx, qid)
			if err != nil {
				return err
			}
			if quiz.ShuffleOptions {
				rand.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO quiz_attempt_questions (attempt_id, position, question_id, option_order)
				VALUES ($1, $2, $3, $4)`, attemptID, i, qid, order); err != nil {
				return err
			}
		}
		created = true
		return nil
	})
	if err != nil {
		return Attempt{}, false, err
	}
	a, err := s.GetAttempt(ctx, attemptID)
	return a, created, err
}

// drawQuestions resolves a quiz's slots into question ids: fixed questions
// in place, category draws at random among questions not already used.
func drawQuestions(ctx context.Context, tx pgx.Tx, activityID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		SELECT question_id, category_id, draw_count
		  FROM quiz_slots WHERE activity_id = $1
		 ORDER BY position, id`, activityID)
	if err != nil {
		return nil, err
	}
	type slot struct {
		question, category *uuid.UUID
		draw               *int
	}
	slots := []slot{}
	for rows.Next() {
		var sl slot
		if err := rows.Scan(&sl.question, &sl.category, &sl.draw); err != nil {
			rows.Close()
			return nil, err
		}
		slots = append(slots, sl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, httperror.Conflict("this quiz has no questions yet")
	}
	used := []uuid.UUID{}
	seen := map[uuid.UUID]bool{}
	for _, sl := range slots {
		if sl.question != nil {
			used = append(used, *sl.question)
			seen[*sl.question] = true
		}
	}
	out := []uuid.UUID{}
	for _, sl := range slots {
		if sl.question != nil {
			out = append(out, *sl.question)
			continue
		}
		drawRows, err := tx.Query(ctx, `
			SELECT id FROM questions
			 WHERE category_id = $1 AND NOT (id = ANY($2))
			 ORDER BY random() LIMIT $3`, *sl.category, used, *sl.draw)
		if err != nil {
			return nil, err
		}
		n := 0
		for drawRows.Next() {
			var id uuid.UUID
			if err := drawRows.Scan(&id); err != nil {
				drawRows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				used = append(used, id)
				out = append(out, id)
				n++
			}
		}
		drawRows.Close()
		if err := drawRows.Err(); err != nil {
			return nil, err
		}
		if n < *sl.draw {
			return nil, httperror.Conflict("this quiz is not ready: a category has too few questions")
		}
	}
	return out, nil
}

func optionIDs(ctx context.Context, tx pgx.Tx, questionID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM question_options WHERE question_id = $1 ORDER BY position, id`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AttemptOwner resolves an attempt's course and owner for authorization.
func (s *Service) AttemptOwner(ctx context.Context, attemptID uuid.UUID) (Course, uuid.UUID, error) {
	var owner uuid.UUID
	var c Course
	err := s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`, qa.user_id
		  FROM quiz_attempts qa
		  JOIN activities a ON a.id = qa.activity_id
		  JOIN sections s ON s.id = a.section_id
		  JOIN courses c ON c.id = s.course_id
		 WHERE qa.id = $1`, attemptID,
	).Scan(append(courseDest(&c), &owner)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, uuid.Nil, httperror.NotFound("attempt")
	}
	return c, owner, err
}

// GetAttempt loads an attempt with its questions in frozen order. Results
// (correct flags, correct options, feedback) appear only once submitted.
func (s *Service) GetAttempt(ctx context.Context, attemptID uuid.UUID) (Attempt, error) {
	var a Attempt
	sum, err := scanAttemptSummary(s.pool.QueryRow(ctx, `
		SELECT `+attemptCols+` FROM quiz_attempts WHERE id = $1`, attemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return a, httperror.NotFound("attempt")
	}
	if err != nil {
		return a, err
	}
	a.AttemptSummary = sum
	if err := s.pool.QueryRow(ctx, `SELECT pass_percent FROM activities WHERE id = $1`, sum.ActivityID).
		Scan(&a.PassPercent); err != nil {
		return a, err
	}
	submitted := sum.SubmittedAt != nil

	rows, err := s.pool.Query(ctx, `
		SELECT aq.question_id, aq.position, q.kind, q.prompt, q.feedback, q.points,
		       aq.option_order, aq.chosen_option_ids, aq.correct, aq.points_awarded,
		       q.media_id, q.feedback_media_id, q.hotspot_clicks, aq.taps
		  FROM quiz_attempt_questions aq
		  JOIN questions q ON q.id = aq.question_id
		 WHERE aq.attempt_id = $1
		 ORDER BY aq.position`, attemptID)
	if err != nil {
		return a, err
	}
	a.Questions = []AttemptQuestion{}
	orders := [][]uuid.UUID{}
	bank := []Question{}
	for rows.Next() {
		var q AttemptQuestion
		var feedback string
		var order []uuid.UUID
		var taps []byte
		stub := Question{Options: []QuestionOption{}}
		if err := rows.Scan(&q.QuestionID, &q.Position, &q.Kind, &q.Prompt, &feedback, &q.Points,
			&order, &q.ChosenOptionIDs, &q.Correct, &q.PointsAwarded,
			&stub.MediaID, &stub.FeedbackMediaID, &stub.HotspotClicks, &taps); err != nil {
			rows.Close()
			return a, err
		}
		if q.ChosenOptionIDs == nil {
			q.ChosenOptionIDs = []uuid.UUID{}
		}
		if len(taps) > 0 {
			if err := json.Unmarshal(taps, &q.Taps); err != nil {
				rows.Close()
				return a, err
			}
		}
		if submitted {
			q.Feedback = &feedback
		} else {
			q.Correct, q.PointsAwarded = nil, nil
		}
		stub.ID = q.QuestionID
		a.Questions = append(a.Questions, q)
		orders = append(orders, order)
		bank = append(bank, stub)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return a, err
	}

	if err := s.attachOptions(ctx, bank); err != nil {
		return a, err
	}
	for i := range a.Questions {
		aq := &a.Questions[i]
		aq.Media = bank[i].Media
		if aq.Kind == domain.QuestionHotspot {
			aq.MaxTaps = maxTaps(bank[i].HotspotClicks, bank[i].Regions)
			if aq.Taps == nil {
				aq.Taps = []Point{}
			}
			if submitted {
				aq.Regions = bank[i].Regions
			}
		}
		if submitted {
			aq.FeedbackMedia = bank[i].FeedbackMedia
		}
		byID := map[uuid.UUID]QuestionOption{}
		for _, o := range bank[i].Options {
			byID[o.ID] = o
		}
		// Frozen order first; options added after the attempt started go last.
		placed := map[uuid.UUID]bool{}
		opts := []AttemptOption{}
		if aq.Kind != domain.QuestionHotspot {
			for _, id := range orders[i] {
				if o, ok := byID[id]; ok {
					opts = append(opts, AttemptOption{ID: o.ID, Label: o.Label, Media: o.Media})
					placed[id] = true
				}
			}
			for _, o := range bank[i].Options {
				if !placed[o.ID] {
					opts = append(opts, AttemptOption{ID: o.ID, Label: o.Label, Media: o.Media})
				}
			}
		}
		a.Questions[i].Options = opts
		if submitted {
			a.Questions[i].CorrectOptionIDs = []uuid.UUID{}
			for _, o := range bank[i].Options {
				if o.Correct {
					a.Questions[i].CorrectOptionIDs = append(a.Questions[i].CorrectOptionIDs, o.ID)
				}
			}
		}
	}
	return a, nil
}

// markQuestion decides whether chosen answers a question correctly. Single
// and multiple choice are all-or-nothing: the chosen set must equal the
// correct set exactly.
func markQuestion(kind string, chosen, correct []uuid.UUID) bool {
	switch kind {
	case domain.QuestionSingle, domain.QuestionMultiple:
		if len(correct) == 0 || len(chosen) != len(correct) {
			return false
		}
		want := map[uuid.UUID]bool{}
		for _, id := range correct {
			want[id] = true
		}
		for _, id := range chosen {
			if !want[id] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// SubmitAttempt marks an open attempt, records the score and, on a pass,
// completes the quiz for the learner. answers maps question id → chosen
// option ids, taps maps hotspot question id → tapped points; unanswered
// questions score zero.
func (s *Service) SubmitAttempt(ctx context.Context, attemptID, userID uuid.UUID, answers map[uuid.UUID][]uuid.UUID, taps map[uuid.UUID][]Point) (Attempt, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var activityID, owner uuid.UUID
		var submittedAt *time.Time
		var passPercent int
		err := tx.QueryRow(ctx, `
			SELECT qa.activity_id, qa.user_id, qa.submitted_at, a.pass_percent
			  FROM quiz_attempts qa JOIN activities a ON a.id = qa.activity_id
			 WHERE qa.id = $1
			   FOR UPDATE OF qa`, attemptID).Scan(&activityID, &owner, &submittedAt, &passPercent)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != userID) {
			return httperror.NotFound("attempt")
		}
		if err != nil {
			return err
		}
		if submittedAt != nil {
			return httperror.Conflict("this attempt has already been submitted")
		}

		type row struct {
			id      uuid.UUID
			kind    string
			points  int
			options []uuid.UUID
			correct []uuid.UUID
			clicks  *int
		}
		rows, err := tx.Query(ctx, `
			SELECT q.id, q.kind, q.points,
			       ARRAY(SELECT o.id FROM question_options o WHERE o.question_id = q.id),
			       ARRAY(SELECT o.id FROM question_options o WHERE o.question_id = q.id AND o.correct),
			       q.hotspot_clicks
			  FROM quiz_attempt_questions aq JOIN questions q ON q.id = aq.question_id
			 WHERE aq.attempt_id = $1`, attemptID)
		if err != nil {
			return err
		}
		qs := []row{}
		inAttempt := map[uuid.UUID]string{}
		hotspotIDs := []uuid.UUID{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.kind, &r.points, &r.options, &r.correct, &r.clicks); err != nil {
				rows.Close()
				return err
			}
			qs = append(qs, r)
			inAttempt[r.id] = r.kind
			if r.kind == domain.QuestionHotspot {
				hotspotIDs = append(hotspotIDs, r.id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for qid := range answers {
			if _, ok := inAttempt[qid]; !ok {
				return httperror.Validation("an answer refers to a question that is not in this attempt", nil)
			}
		}
		for qid := range taps {
			if inAttempt[qid] != domain.QuestionHotspot {
				return httperror.Validation("taps refer to a question that is not a hotspot question in this attempt", nil)
			}
		}
		regions, err := s.regionsFor(ctx, tx, hotspotIDs)
		if err != nil {
			return err
		}

		score, maxScore := 0, 0
		for _, q := range qs {
			if q.kind == domain.QuestionHotspot {
				pts := taps[q.id]
				if pts == nil {
					pts = []Point{}
				}
				if err := validateTaps(pts, maxTaps(q.clicks, regions[q.id])); err != nil {
					return err
				}
				ok := markHotspot(regions[q.id], pts)
				awarded := 0
				if ok {
					awarded = q.points
				}
				score += awarded
				maxScore += q.points
				raw, err := json.Marshal(pts)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `
					UPDATE quiz_attempt_questions
					   SET taps = $3, correct = $4, points_awarded = $5
					 WHERE attempt_id = $1 AND question_id = $2`, attemptID, q.id, raw, ok, awarded); err != nil {
					return err
				}
				continue
			}
			valid := map[uuid.UUID]bool{}
			for _, id := range q.options {
				valid[id] = true
			}
			chosen := []uuid.UUID{}
			dup := map[uuid.UUID]bool{}
			for _, id := range answers[q.id] {
				if !valid[id] {
					return httperror.Validation("an answer refers to an option that does not belong to its question", nil)
				}
				if !dup[id] {
					dup[id] = true
					chosen = append(chosen, id)
				}
			}
			ok := markQuestion(q.kind, chosen, q.correct)
			awarded := 0
			if ok {
				awarded = q.points
			}
			score += awarded
			maxScore += q.points
			if _, err := tx.Exec(ctx, `
				UPDATE quiz_attempt_questions
				   SET chosen_option_ids = $3, correct = $4, points_awarded = $5
				 WHERE attempt_id = $1 AND question_id = $2`, attemptID, q.id, chosen, ok, awarded); err != nil {
				return err
			}
		}
		passed := maxScore > 0 && score*100 >= passPercent*maxScore
		if _, err := tx.Exec(ctx, `
			UPDATE quiz_attempts SET submitted_at = now(), score = $2, max_score = $3, passed = $4,
			       cycle = `+activityCycleOf("quiz_attempts.activity_id", "quiz_attempts.user_id")+`
			 WHERE id = $1`, attemptID, score, maxScore, passed); err != nil {
			return err
		}
		if passed {
			return completeActivity(ctx, tx, activityID, userID)
		}
		return nil
	})
	if err != nil {
		return Attempt{}, err
	}
	return s.GetAttempt(ctx, attemptID)
}

// ResetResult counts what a quiz reset deleted.
type ResetResult struct {
	Attempts    int64 `json:"attempts"`
	Completions int64 `json:"completions"`
}

// ResetAttempts deletes the attempts and completions of a quiz in each
// person's current cycle (and any open attempt). Earlier cycles, and every
// certificate already issued, stay untouched; their attempts keep the
// questions they used locked.
func (s *Service) ResetAttempts(ctx context.Context, activityID uuid.UUID) (ResetResult, error) {
	var res ResetResult
	if _, err := s.QuizActivity(ctx, activityID); err != nil {
		return res, err
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM quiz_attempts qa
			 WHERE qa.activity_id = $1
			   AND (qa.submitted_at IS NULL OR qa.cycle = `+activityCycleOf("qa.activity_id", "qa.user_id")+`)`, activityID)
		if err != nil {
			return err
		}
		res.Attempts = tag.RowsAffected()
		tag, err = tx.Exec(ctx, `
			DELETE FROM completions c
			 WHERE c.activity_id = $1 AND c.cycle = `+activityCycleOf("c.activity_id", "c.user_id"), activityID)
		res.Completions = tag.RowsAffected()
		return err
	})
	return res, err
}

// ----------------------------------------------------------------------------
// Gradebook
// ----------------------------------------------------------------------------

// GradebookQuiz is one column of the gradebook.
type GradebookQuiz struct {
	ActivityID  uuid.UUID `json:"activity_id"`
	Title       string    `json:"title"`
	PassPercent int       `json:"pass_percent"`
	MaxAttempts *int      `json:"max_attempts"`
}

// GradebookCell is one learner on one quiz.
type GradebookCell struct {
	ActivityID  uuid.UUID `json:"activity_id"`
	Attempts    int       `json:"attempts"`
	BestPercent *int      `json:"best_percent"`
	Passed      bool      `json:"passed"`
	InProgress  bool      `json:"in_progress"`
}

// GradebookRow is one learner across all quizzes, in column order.
type GradebookRow struct {
	UserID      uuid.UUID       `json:"user_id"`
	DisplayName string          `json:"display_name"`
	Email       string          `json:"email"`
	Cells       []GradebookCell `json:"cells"`
	DueAt       *time.Time      `json:"due_at"`
	Overdue     bool            `json:"overdue"`
	Progress    Progress        `json:"progress"`
}

// Gradebook is learners × quizzes for one course.
type Gradebook struct {
	Quizzes []GradebookQuiz `json:"quizzes"`
	Rows    []GradebookRow  `json:"rows"`
}

// Gradebook builds the course gradebook: every learner enrolment against
// every quiz, with best percent, pass and submitted attempt count.
func (s *Service) Gradebook(ctx context.Context, courseID uuid.UUID) (Gradebook, error) {
	g := Gradebook{Quizzes: []GradebookQuiz{}, Rows: []GradebookRow{}}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title, a.pass_percent, a.max_attempts
		  FROM activities a JOIN sections s ON s.id = a.section_id
		 WHERE s.course_id = $1 AND a.kind = 'quiz'
		 ORDER BY s.position, s.created_at, a.position, a.created_at`, courseID)
	if err != nil {
		return g, err
	}
	col := map[uuid.UUID]int{}
	for rows.Next() {
		var q GradebookQuiz
		if err := rows.Scan(&q.ActivityID, &q.Title, &q.PassPercent, &q.MaxAttempts); err != nil {
			rows.Close()
			return g, err
		}
		col[q.ActivityID] = len(g.Quizzes)
		g.Quizzes = append(g.Quizzes, q)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return g, err
	}

	rows, err = s.pool.Query(ctx, `
		SELECT u.id, u.display_name, COALESCE(u.email, ''), e.due_at
		  FROM enrolments e JOIN users u ON u.id = e.user_id
		 WHERE e.course_id = $1 AND e.role = $2 AND e.status = 'active'
		 ORDER BY u.display_name`, courseID, domain.EnrolmentLearner)
	if err != nil {
		return g, err
	}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		var r GradebookRow
		if err := rows.Scan(&r.UserID, &r.DisplayName, &r.Email, &r.DueAt); err != nil {
			rows.Close()
			return g, err
		}
		r.Cells = make([]GradebookCell, len(g.Quizzes))
		for i, q := range g.Quizzes {
			r.Cells[i] = GradebookCell{ActivityID: q.ActivityID}
		}
		index[r.UserID] = len(g.Rows)
		g.Rows = append(g.Rows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return g, err
	}
	now := time.Now()
	for i := range g.Rows {
		p, err := s.progressFor(ctx, courseID, g.Rows[i].UserID)
		if err != nil {
			return g, err
		}
		g.Rows[i].Progress = p
		g.Rows[i].Overdue = isOverdue(g.Rows[i].DueAt, p, now)
	}

	rows, err = s.pool.Query(ctx, `
		SELECT qa.user_id, qa.activity_id,
		       count(*) FILTER (WHERE qa.submitted_at IS NOT NULL),
		       max(qa.score * 100 / NULLIF(qa.max_score, 0)) FILTER (WHERE qa.submitted_at IS NOT NULL),
		       COALESCE(bool_or(qa.passed), FALSE),
		       bool_or(qa.submitted_at IS NULL)
		  FROM quiz_attempts qa
		  JOIN activities a ON a.id = qa.activity_id
		  JOIN sections s ON s.id = a.section_id
		  JOIN enrolments e ON e.course_id = s.course_id AND e.user_id = qa.user_id
		 WHERE s.course_id = $1 AND qa.cycle = e.cycle
		 GROUP BY qa.user_id, qa.activity_id`, courseID)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var userID, activityID uuid.UUID
		var cell GradebookCell
		if err := rows.Scan(&userID, &activityID, &cell.Attempts, &cell.BestPercent, &cell.Passed, &cell.InProgress); err != nil {
			return g, err
		}
		ri, okRow := index[userID]
		ci, okCol := col[activityID]
		if !okRow || !okCol {
			continue
		}
		cell.ActivityID = activityID
		g.Rows[ri].Cells[ci] = cell
	}
	return g, rows.Err()
}
