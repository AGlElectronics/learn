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
// left its synced group) becomes an active manual one, and enrolling an
// active learner as trainer makes them a trainer. Who may grant which role
// is the handler's decision (trainer needs staff.manage).
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
			    OR (EXCLUDED.role = 'trainer' AND enrolments.role = 'learner')
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
	if !course.Published || !course.SelfEnrol || course.Archived() {
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

// EnrolmentRow is the authorization view of one enrolment.
type EnrolmentRow struct {
	ID       uuid.UUID
	CourseID uuid.UUID
	UserID   uuid.UUID
	Role     string
	Status   string
}

// GetEnrolment loads one enrolment (any status) or a typed 404.
func (s *Service) GetEnrolment(ctx context.Context, enrolmentID uuid.UUID) (EnrolmentRow, error) {
	var e EnrolmentRow
	err := s.pool.QueryRow(ctx, `SELECT id, course_id, user_id, role, status FROM enrolments WHERE id = $1`, enrolmentID).
		Scan(&e.ID, &e.CourseID, &e.UserID, &e.Role, &e.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, httperror.NotFound("enrolment")
	}
	return e, err
}

// ErrLastTrainer refuses to remove the last active trainer of a course.
var ErrLastTrainer = httperror.Conflict("this is the course's last trainer; add another trainer first")

// Unenrol removes an enrolment. Completions are kept — re-enrolling
// restores the person's progress. The last active trainer of a course can
// never be removed; the trainer rows are locked so two removals at once
// can't both pass the check.
func (s *Service) Unenrol(ctx context.Context, enrolmentID uuid.UUID) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var courseID uuid.UUID
		var role, status string
		err := tx.QueryRow(ctx, `SELECT course_id, role, status FROM enrolments WHERE id = $1`, enrolmentID).
			Scan(&courseID, &role, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("enrolment")
		}
		if err != nil {
			return err
		}
		if role == domain.EnrolmentTrainer && status == EnrolmentStatusActive {
			rows, err := tx.Query(ctx, `
				SELECT id FROM enrolments
				 WHERE course_id = $1 AND role = 'trainer' AND status = 'active'
				 FOR UPDATE`, courseID)
			if err != nil {
				return err
			}
			trainers, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			if len(trainers) <= 1 {
				return ErrLastTrainer
			}
		}
		tag, err := tx.Exec(ctx, `DELETE FROM enrolments WHERE id = $1`, enrolmentID)
		if err == nil && tag.RowsAffected() == 0 {
			return httperror.NotFound("enrolment")
		}
		return err
	})
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

// PeopleQuery is a people-picker search.
type PeopleQuery struct {
	Q string
	// Organizations limits the result to people of these organizations.
	// Empty finds nobody.
	Organizations []uuid.UUID
	// WithEmail returns (and matches on) email addresses. Off for callers
	// who may only see names (trainers).
	WithEmail bool
	Limit     int
}

// SearchPeople finds users for the enrolment and course-role pickers:
// people Learn knows (signed in once, or synced from a group) who belong to
// one of the caller's organizations. Erased people never appear.
func (s *Service) SearchPeople(ctx context.Context, pq PeopleQuery) ([]Person, error) {
	if pq.Limit <= 0 || pq.Limit > 50 {
		pq.Limit = 20
	}
	out := []Person{}
	if len(pq.Organizations) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.display_name, COALESCE(u.email, '')
		  FROM users u
		 WHERE u.erased_at IS NULL
		   AND EXISTS (SELECT 1 FROM user_organizations uo WHERE uo.user_id = u.id AND uo.organization_id = ANY($2))
		   AND ($1 = '' OR u.display_name ILIKE '%' || $1 || '%' OR ($3 AND u.email ILIKE '%' || $1 || '%'))
		 ORDER BY u.display_name
		 LIMIT $4
	`, pq.Q, pq.Organizations, pq.WithEmail, pq.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.DisplayName, &p.Email); err != nil {
			return nil, err
		}
		if !pq.WithEmail {
			p.Email = ""
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// InOrganization reports whether a (not erased) person belongs to orgID.
func (s *Service) InOrganization(ctx context.Context, userID, orgID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_organizations uo JOIN users u ON u.id = uo.user_id
		                WHERE uo.user_id = $1 AND uo.organization_id = $2 AND u.erased_at IS NULL)`,
		userID, orgID).Scan(&ok)
	return ok, err
}

// StaffedCourse is one course with its trainers, for the course-role page.
type StaffedCourse struct {
	ID        uuid.UUID     `json:"id"`
	Title     string        `json:"title"`
	Published bool          `json:"published"`
	Archived  bool          `json:"archived"`
	Trainers  []CourseStaff `json:"trainers"`
}

// CourseStaff is one trainer on a course.
type CourseStaff struct {
	EnrolmentID uuid.UUID `json:"enrolment_id"`
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email,omitempty"`
}

// ListCourseStaff returns every course of the organizations with its active
// trainers, filtered by title when q is set.
func (s *Service) ListCourseStaff(ctx context.Context, orgs []uuid.UUID, q string) ([]StaffedCourse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.title, c.published, c.archived_at IS NOT NULL,
		       e.id, u.id, u.display_name, COALESCE(u.email, '')
		  FROM courses c
		  LEFT JOIN enrolments e ON e.course_id = c.id AND e.role = 'trainer' AND e.status = 'active'
		  LEFT JOIN users u ON u.id = e.user_id
		 WHERE c.organization_id = ANY($1)
		   AND ($2 = '' OR c.title ILIKE '%' || $2 || '%')
		 ORDER BY c.title, c.id, u.display_name`, orgs, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StaffedCourse{}
	for rows.Next() {
		var c StaffedCourse
		var eID, uID *uuid.UUID
		var name, email *string
		if err := rows.Scan(&c.ID, &c.Title, &c.Published, &c.Archived, &eID, &uID, &name, &email); err != nil {
			return nil, err
		}
		if n := len(out); n == 0 || out[n-1].ID != c.ID {
			c.Trainers = []CourseStaff{}
			out = append(out, c)
		}
		if eID != nil {
			last := &out[len(out)-1]
			last.Trainers = append(last.Trainers, CourseStaff{EnrolmentID: *eID, UserID: *uID, DisplayName: *name, Email: *email})
		}
	}
	return out, rows.Err()
}
