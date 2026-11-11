// Package learn is the course-delivery service layer: courses made of
// ordered sections and page activities, enrolments, and per-activity
// completions. Handlers own authorization (permissions come from the access
// config, per-course staffing from enrolments); this package owns state and
// invariants and returns typed httperror values for business failures.
package learn

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Service is safe for concurrent use.
type Service struct {
	pool *pgxpool.Pool
	// feedbackPepper keys the anonymous-feedback dedupe hash.
	feedbackPepper []byte
}

// NewService constructs the course service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Course is one course row.
type Course struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Title          string     `json:"title"`
	Summary        string     `json:"summary"`
	Published      bool       `json:"published"`
	SelfEnrol      bool       `json:"self_enrol"`
	CreatedBy      *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// DefaultLang is the language of the base text; Languages are the ones
	// learners may get (always including DefaultLang).
	DefaultLang string   `json:"default_lang"`
	Languages   []string `json:"languages"`
	// Lang is the language this payload was served in (learner views only).
	Lang string `json:"lang,omitempty"`
	// ArchivedAt is set while the course is archived: hidden from learners
	// and course lists, read-only for staff.
	ArchivedAt *time.Time `json:"archived_at"`
}

// Archived reports whether the course is archived.
func (c Course) Archived() bool { return c.ArchivedAt != nil }

// Progress is completed pages over total pages for one person on one course.
type Progress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
}

// CourseSummary is a course in a list, annotated for the calling user.
type CourseSummary struct {
	Course
	// EnrolmentRole is the caller's enrolment role ("" when not enrolled).
	EnrolmentRole string   `json:"enrolment_role,omitempty"`
	Progress      Progress `json:"progress"`
	// DueAt is the caller's due date; Overdue = past it with work left.
	DueAt   *time.Time `json:"due_at,omitempty"`
	Overdue bool       `json:"overdue"`
	// Locked: a sequential learning path keeps this course closed until
	// the courses in Needs are finished.
	Locked bool     `json:"locked"`
	Needs  []string `json:"needs,omitempty"`
}

// Section is one ordered section of a course.
type Section struct {
	ID       uuid.UUID `json:"id"`
	CourseID uuid.UUID `json:"course_id"`
	Title    string    `json:"title"`
	Position int       `json:"position"`
}

// Activity is one activity inside a section: a page, or a quiz (whose body
// is its intro text).
type Activity struct {
	ID        uuid.UUID `json:"id"`
	SectionID uuid.UUID `json:"section_id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Position  int       `json:"position"`
	// Quiz settings; stored for every activity, meaningful for quizzes.
	PassPercent    int  `json:"pass_percent"`
	MaxAttempts    *int `json:"max_attempts"`
	ShuffleOptions bool `json:"shuffle_options"`
	// Completed is the calling user's completion state.
	Completed bool `json:"completed"`
	// Quiz is the calling user's standing on a quiz (nil for pages).
	Quiz *QuizStanding `json:"quiz,omitempty"`
	// Prerequisites are the activity ids that must be complete first.
	// Locked (with the titles still needed) hides the body from learners.
	Prerequisites []uuid.UUID `json:"prerequisites"`
	Locked        bool        `json:"locked"`
	Needs         []string    `json:"needs,omitempty"`
}

const activityCols = `id, section_id, kind, title, body, position, pass_percent, max_attempts, shuffle_options`

func scanActivity(row pgx.Row) (Activity, error) {
	var a Activity
	err := row.Scan(&a.ID, &a.SectionID, &a.Kind, &a.Title, &a.Body, &a.Position, &a.PassPercent, &a.MaxAttempts, &a.ShuffleOptions)
	return a, err
}

// SectionDetail is a section plus its activities in order.
type SectionDetail struct {
	Section
	Activities []Activity `json:"activities"`
}

// CourseDetail is the full course as one user sees it.
type CourseDetail struct {
	Course
	// EnrolmentRole is the caller's enrolment role ("" when not enrolled).
	EnrolmentRole string          `json:"enrolment_role,omitempty"`
	Sections      []SectionDetail `json:"sections"`
	Progress      Progress        `json:"progress"`
	// DueDays is the course setting; DueAt/Overdue are the caller's.
	DueDays *int       `json:"due_days"`
	DueAt   *time.Time `json:"due_at,omitempty"`
	Overdue bool       `json:"overdue"`
	// AwardsQualification is what finishing the course awards (nil = none);
	// Cycle is the caller's renewal cycle (1 = first time through).
	AwardsQualification *QualificationRef `json:"awards_qualification"`
	Cycle               int               `json:"cycle"`
}

// Enrolment is one person on one course.
type Enrolment struct {
	ID          uuid.UUID `json:"id"`
	CourseID    uuid.UUID `json:"course_id"`
	UserID      uuid.UUID `json:"user_id"`
	Role        string    `json:"role"`
	DisplayName string    `json:"display_name"`
	// Email is left out for callers who may not see it (trainers).
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Progress  Progress  `json:"progress"`
	// Source is manual or group (created by the Keycloak group sync);
	// Status inactive means they left the synced group: no access, progress
	// kept, rejoining reactivates.
	Source      string     `json:"source"`
	Status      string     `json:"status"`
	LeftGroupAt *time.Time `json:"left_group_at"`
	// DueAt is set from the course's due_days at enrolment; staff can override.
	DueAt   *time.Time `json:"due_at"`
	Overdue bool       `json:"overdue"`
}

// Person is a users row projected for the enrolment picker. Email is
// empty for callers who may not see it. A person found only in the
// identity provider's directory has no ID yet and carries DirectoryID.
type Person struct {
	ID          *uuid.UUID `json:"id"`
	DisplayName string     `json:"display_name"`
	Email       string     `json:"email,omitempty"`
	DirectoryID string     `json:"directory_id,omitempty"`
}

const courseCols = `id, organization_id, title, summary, published, self_enrol, created_by, created_at, updated_at, default_lang, languages, archived_at`

// courseDest is the scan destination list matching courseCols.
func courseDest(c *Course) []any {
	return []any{&c.ID, &c.OrganizationID, &c.Title, &c.Summary, &c.Published, &c.SelfEnrol, &c.CreatedBy,
		&c.CreatedAt, &c.UpdatedAt, &c.DefaultLang, &c.Languages, &c.ArchivedAt}
}

func scanCourse(row pgx.Row) (Course, error) {
	var c Course
	err := row.Scan(courseDest(&c)...)
	return c, err
}

// GetCourse loads one course or a typed 404.
func (s *Service) GetCourse(ctx context.Context, id uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `SELECT `+courseCols+` FROM courses WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("course")
	}
	return c, err
}

// IsStaff reports whether userID has an active trainer enrolment on the course.
func (s *Service) IsStaff(ctx context.Context, courseID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM enrolments WHERE course_id = $1 AND user_id = $2 AND role = $3 AND status = 'active')`,
		courseID, userID, domain.EnrolmentTrainer,
	).Scan(&ok)
	return ok, err
}

// EnrolmentRole returns the user's enrolment role on a course ("" if none,
// if the enrolment is inactive because they left its synced group, or if
// the course is archived: an archived course is closed to learners).
func (s *Service) EnrolmentRole(ctx context.Context, courseID, userID uuid.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT e.role FROM enrolments e JOIN courses c ON c.id = e.course_id
		 WHERE e.course_id = $1 AND e.user_id = $2 AND e.status = 'active' AND c.archived_at IS NULL`,
		courseID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

// progressFor computes one user's progress on one course.
func (s *Service) progressFor(ctx context.Context, courseID, userID uuid.UUID) (Progress, error) {
	var p Progress
	err := s.pool.QueryRow(ctx, `
		SELECT count(a.id),
		       count(c.id)
		  FROM activities a
		  JOIN sections s ON s.id = a.section_id
		  LEFT JOIN completions c ON c.activity_id = a.id AND c.user_id = $2 AND c.cycle = `+cycleOf("$1", "$2")+`
		 WHERE s.course_id = $1
	`, courseID, userID).Scan(&p.Total, &p.Completed)
	return p, err
}
