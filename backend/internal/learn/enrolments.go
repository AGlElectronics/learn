package learn

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// ListEnrolments returns everyone on a course with their progress.
func (s *Service) ListEnrolments(ctx context.Context, courseID uuid.UUID) ([]Enrolment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.course_id, e.user_id, e.role, u.display_name, COALESCE(u.email, ''), e.created_at,
		       e.source, e.status, e.left_group_at, e.due_at
		  FROM enrolments e
		  JOIN users u ON u.id = e.user_id
		 WHERE e.course_id = $1
		 ORDER BY e.status, e.role DESC, u.display_name
	`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Enrolment{}
	for rows.Next() {
		var e Enrolment
		if err := rows.Scan(&e.ID, &e.CourseID, &e.UserID, &e.Role, &e.DisplayName, &e.Email, &e.CreatedAt,
			&e.Source, &e.Status, &e.LeftGroupAt, &e.DueAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		p, err := s.progressFor(ctx, courseID, out[i].UserID)
		if err != nil {
			return nil, err
		}
		out[i].Progress = p
		out[i].Overdue = out[i].Role == domain.EnrolmentLearner && out[i].Status == "active" &&
			isOverdue(out[i].DueAt, p, time.Now())
	}
	return out, nil
}

// Enrol adds a person to a course. The person must have logged in at least
// once, or been synced from a Keycloak group. An inactive enrolment (they
// left its synced group) becomes an active manual one.
func (s *Service) Enrol(ctx context.Context, courseID, userID uuid.UUID, role string) (Enrolment, error) {
	if role != domain.EnrolmentLearner && role != domain.EnrolmentTrainer {
		return Enrolment{}, httperror.Validation("role must be learner or trainer", nil)
	}
	var e Enrolment
	err := s.pool.QueryRow(ctx, `
		WITH ins AS (
			INSERT INTO enrolments (course_id, user_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (course_id, user_id) DO UPDATE
			   SET role = EXCLUDED.role, source = 'manual', status = 'active', left_group_at = NULL
			 WHERE enrolments.status = 'inactive'
			RETURNING id, course_id, user_id, role, created_at, source, status, left_group_at
		)
		SELECT ins.id, ins.course_id, ins.user_id, ins.role, u.display_name, COALESCE(u.email, ''), ins.created_at,
		       ins.source, ins.status, ins.left_group_at
		  FROM ins JOIN users u ON u.id = ins.user_id
	`, courseID, userID, role).Scan(&e.ID, &e.CourseID, &e.UserID, &e.Role, &e.DisplayName, &e.Email, &e.CreatedAt,
		&e.Source, &e.Status, &e.LeftGroupAt)
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows), errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation
		return Enrolment{}, httperror.Conflict("this person is already enrolled")
	case errors.As(err, &pgErr) && pgErr.Code == "23503": // foreign_key_violation
		return Enrolment{}, httperror.NotFound("user")
	}
	return e, err
}

// SelfEnrol enrols the caller as a learner on a published self-enrol course.
// The published/self_enrol checks run here, not in the handler, so they hold
// under concurrent updates.
func (s *Service) SelfEnrol(ctx context.Context, courseID, userID uuid.UUID) (Enrolment, error) {
	course, err := s.GetCourse(ctx, courseID)
	if err != nil {
		return Enrolment{}, err
	}
	if !course.Published || !course.SelfEnrol {
		return Enrolment{}, httperror.New(httperror.CodeForbidden, "this course does not allow self-enrolment")
	}
	return s.Enrol(ctx, courseID, userID, domain.EnrolmentLearner)
}

// EnrolmentCourse resolves an enrolment's course for authorization.
func (s *Service) EnrolmentCourse(ctx context.Context, enrolmentID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c JOIN enrolments e ON e.course_id = c.id
		 WHERE e.id = $1`, enrolmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("enrolment")
	}
	return c, err
}

// Unenrol removes an enrolment. Completions are kept — re-enrolling
// restores the person's progress.
func (s *Service) Unenrol(ctx context.Context, enrolmentID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM enrolments WHERE id = $1`, enrolmentID)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("enrolment")
	}
	return err
}

// SetCompleted marks or unmarks one activity complete for one user in their
// current cycle; finishing the course issues its certificate. Quizzes
// complete only by passing an attempt.
func (s *Service) SetCompleted(ctx context.Context, activityID, userID uuid.UUID, completed bool) error {
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM activities WHERE id = $1`, activityID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return httperror.NotFound("activity")
	}
	if err != nil {
		return err
	}
	if kind == domain.ActivityQuiz {
		return httperror.BadRequest("quizzes are completed by passing an attempt")
	}
	if kind == domain.ActivitySession {
		return httperror.BadRequest("sessions are completed by attending")
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if completed {
			return completeActivity(ctx, tx, activityID, userID)
		}
		return uncompleteActivity(ctx, tx, activityID, userID)
	})
}

// SearchPeople finds users for the enrolment picker. Only people who have
// signed in at least once exist here — Learn provisions users on first
// login and never reads the HR database.
func (s *Service) SearchPeople(ctx context.Context, q string, limit int) ([]Person, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, display_name, COALESCE(email, '')
		  FROM users
		 WHERE $1 = '' OR display_name ILIKE '%' || $1 || '%' OR email ILIKE '%' || $1 || '%'
		 ORDER BY display_name
		 LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.Email); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
