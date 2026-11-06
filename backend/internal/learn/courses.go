package learn

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/domain"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// listSummaries runs a query returning course rows annotated with the
// caller's enrolment role, then attaches progress.
func (s *Service) listSummaries(ctx context.Context, userID uuid.UUID, query string, args ...any) ([]CourseSummary, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CourseSummary{}
	for rows.Next() {
		var cs CourseSummary
		var role *string
		if err := rows.Scan(append(courseDest(&cs.Course), &role)...); err != nil {
			return nil, err
		}
		if role != nil {
			cs.EnrolmentRole = *role
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		p, err := s.progressFor(ctx, out[i].ID, userID)
		if err != nil {
			return nil, err
		}
		out[i].Progress = p
	}
	if err := s.attachCourseLocks(ctx, out, userID); err != nil {
		return nil, err
	}
	return out, s.attachSummaryDue(ctx, out, userID)
}

// ListMine returns the courses the user is enrolled in (any role).
// Unpublished courses only appear for their staff.
func (s *Service) ListMine(ctx context.Context, userID uuid.UUID) ([]CourseSummary, error) {
	return s.listSummaries(ctx, userID, `
		SELECT `+prefixedCourseCols("c")+`, e.role
		  FROM courses c
		  JOIN enrolments e ON e.course_id = c.id AND e.user_id = $1 AND e.status = 'active'
		 WHERE c.published OR e.role = '`+domain.EnrolmentTrainer+`'
		 ORDER BY c.title`, userID)
}

// ListCatalog returns published self-enrol courses, annotated with the
// caller's enrolment (so the UI can say "already enrolled").
func (s *Service) ListCatalog(ctx context.Context, userID uuid.UUID) ([]CourseSummary, error) {
	return s.listSummaries(ctx, userID, `
		SELECT `+prefixedCourseCols("c")+`, e.role
		  FROM courses c
		  LEFT JOIN enrolments e ON e.course_id = c.id AND e.user_id = $1 AND e.status = 'active'
		 WHERE c.published AND c.self_enrol
		 ORDER BY c.title`, userID)
}

// ListTeach returns courses the user staffs, plus every course of the
// organizations in manageOrgs (the orgs where they hold course.manage).
func (s *Service) ListTeach(ctx context.Context, userID uuid.UUID, manageOrgs []uuid.UUID) ([]CourseSummary, error) {
	return s.listSummaries(ctx, userID, `
		SELECT `+prefixedCourseCols("c")+`, e.role
		  FROM courses c
		  LEFT JOIN enrolments e ON e.course_id = c.id AND e.user_id = $1 AND e.status = 'active'
		 WHERE e.role = '`+domain.EnrolmentTrainer+`' OR c.organization_id = ANY($2)
		 ORDER BY c.title`, userID, manageOrgs)
}

// CreateCourseInput is the validated create payload.
type CreateCourseInput struct {
	OrganizationID uuid.UUID
	Title          string
	Summary        string
	SelfEnrol      bool
}

// CreateCourse inserts the course and makes the creator course staff
// (a trainer enrolment) in the same transaction.
func (s *Service) CreateCourse(ctx context.Context, userID uuid.UUID, in CreateCourseInput) (Course, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Course{}, httperror.Validation("title is required", nil)
	}
	var c Course
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		c, err = scanCourse(tx.QueryRow(ctx, `
			INSERT INTO courses (organization_id, title, summary, self_enrol, created_by)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING `+courseCols,
			in.OrganizationID, in.Title, strings.TrimSpace(in.Summary), in.SelfEnrol, userID))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO enrolments (course_id, user_id, role)
			VALUES ($1, $2, $3)`,
			c.ID, userID, domain.EnrolmentTrainer)
		return err
	})
	return c, err
}

// UpdateCourseInput carries optional course fields; nil = unchanged.
type UpdateCourseInput struct {
	Title     *string
	Summary   *string
	SelfEnrol *bool
}

// UpdateCourse patches title, summary and self-enrolment.
func (s *Service) UpdateCourse(ctx context.Context, id uuid.UUID, in UpdateCourseInput) (Course, error) {
	if in.Title != nil {
		*in.Title = strings.TrimSpace(*in.Title)
		if *in.Title == "" {
			return Course{}, httperror.Validation("title must not be empty", nil)
		}
	}
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		UPDATE courses
		   SET title      = COALESCE($2, title),
		       summary    = COALESCE($3, summary),
		       self_enrol = COALESCE($4, self_enrol),
		       updated_at = now()
		 WHERE id = $1
		 RETURNING `+courseCols,
		id, in.Title, in.Summary, in.SelfEnrol))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("course")
	}
	return c, err
}

// SetPublished publishes or unpublishes a course. Publishing fails while
// any quiz in the course is empty or invalid.
func (s *Service) SetPublished(ctx context.Context, id uuid.UUID, published bool) (Course, error) {
	if published {
		if err := s.validateCourseQuizzes(ctx, id); err != nil {
			return Course{}, err
		}
	}
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		UPDATE courses SET published = $2, updated_at = now()
		 WHERE id = $1
		 RETURNING `+courseCols, id, published))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("course")
	}
	return c, err
}

// Detail assembles the full course view for one user: ordered sections and
// activities with the user's completion flags, plus progress.
func (s *Service) Detail(ctx context.Context, course Course, userID uuid.UUID) (CourseDetail, error) {
	d := CourseDetail{Course: course, Sections: []SectionDetail{}}

	role, err := s.EnrolmentRole(ctx, course.ID, userID)
	if err != nil {
		return d, err
	}
	d.EnrolmentRole = role

	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.course_id, s.title, s.position,
		       a.id, a.section_id, a.kind, a.title, a.body, a.position,
		       a.pass_percent, a.max_attempts, a.shuffle_options,
		       (c.id IS NOT NULL) AS completed
		  FROM sections s
		  LEFT JOIN activities a ON a.section_id = s.id
		  LEFT JOIN completions c ON c.activity_id = a.id AND c.user_id = $2 AND c.cycle = `+cycleOf("$1", "$2")+`
		 WHERE s.course_id = $1
		 ORDER BY s.position, s.created_at, a.position, a.created_at
	`, course.ID, userID)
	if err != nil {
		return d, err
	}
	defer rows.Close()

	index := map[uuid.UUID]int{}
	for rows.Next() {
		var sec Section
		var aID, aSection *uuid.UUID
		var aKind, aTitle, aBody *string
		var aPos, aPass, aMax *int
		var aShuffle *bool
		var completed bool
		if err := rows.Scan(
			&sec.ID, &sec.CourseID, &sec.Title, &sec.Position,
			&aID, &aSection, &aKind, &aTitle, &aBody, &aPos,
			&aPass, &aMax, &aShuffle, &completed,
		); err != nil {
			return d, err
		}
		i, ok := index[sec.ID]
		if !ok {
			i = len(d.Sections)
			index[sec.ID] = i
			d.Sections = append(d.Sections, SectionDetail{Section: sec, Activities: []Activity{}})
		}
		if aID != nil {
			a := Activity{
				ID: *aID, SectionID: *aSection, Kind: *aKind, Title: *aTitle, Body: *aBody, Position: *aPos,
				PassPercent: *aPass, MaxAttempts: aMax, ShuffleOptions: *aShuffle, Completed: completed,
			}
			d.Progress.Total++
			if completed {
				d.Progress.Completed++
			}
			d.Sections[i].Activities = append(d.Sections[i].Activities, a)
		}
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows.Close()
	return d, s.attachQuizStandings(ctx, &d, userID)
}

// ----------------------------------------------------------------------------
// Sections
// ----------------------------------------------------------------------------

// CreateSection appends a section to the course.
func (s *Service) CreateSection(ctx context.Context, courseID uuid.UUID, title string) (Section, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Section{}, httperror.Validation("title is required", nil)
	}
	var sec Section
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sections (course_id, title, position)
		VALUES ($1, $2, (SELECT COALESCE(MAX(position), -1) + 1 FROM sections WHERE course_id = $1))
		RETURNING id, course_id, title, position`,
		courseID, title,
	).Scan(&sec.ID, &sec.CourseID, &sec.Title, &sec.Position)
	return sec, err
}

// SectionCourse resolves a section's course for authorization.
func (s *Service) SectionCourse(ctx context.Context, sectionID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c JOIN sections s ON s.course_id = c.id
		 WHERE s.id = $1`, sectionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("section")
	}
	return c, err
}

// UpdateSectionInput carries optional section fields; nil = unchanged.
type UpdateSectionInput struct {
	Title    *string
	Position *int
}

// UpdateSection renames or reorders a section.
func (s *Service) UpdateSection(ctx context.Context, id uuid.UUID, in UpdateSectionInput) (Section, error) {
	if in.Title != nil {
		*in.Title = strings.TrimSpace(*in.Title)
		if *in.Title == "" {
			return Section{}, httperror.Validation("title must not be empty", nil)
		}
	}
	var sec Section
	err := s.pool.QueryRow(ctx, `
		UPDATE sections
		   SET title = COALESCE($2, title), position = COALESCE($3, position), updated_at = now()
		 WHERE id = $1
		 RETURNING id, course_id, title, position`,
		id, in.Title, in.Position,
	).Scan(&sec.ID, &sec.CourseID, &sec.Title, &sec.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return Section{}, httperror.NotFound("section")
	}
	return sec, err
}

// DeleteSection removes a section and (via cascade) its activities.
func (s *Service) DeleteSection(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sections WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("section")
	}
	return err
}

// ----------------------------------------------------------------------------
// Activities (pages and quizzes)
// ----------------------------------------------------------------------------

// CreateActivityInput is the create payload. Kind defaults to page; the
// quiz settings default to the column defaults when nil.
type CreateActivityInput struct {
	Kind           string
	Title          string
	Body           string
	PassPercent    *int
	MaxAttempts    *int
	ShuffleOptions *bool
}

// CreateActivity appends a page or quiz to a section.
func (s *Service) CreateActivity(ctx context.Context, sectionID uuid.UUID, in CreateActivityInput) (Activity, error) {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return Activity{}, httperror.Validation("title is required", nil)
	}
	switch in.Kind {
	case "":
		in.Kind = domain.ActivityPage
	case domain.ActivityPage, domain.ActivityQuiz, domain.ActivitySession:
	default:
		return Activity{}, httperror.Validation("kind must be page, quiz or session", nil)
	}
	if err := validateQuizSettings(in.PassPercent, in.MaxAttempts); err != nil {
		return Activity{}, err
	}
	return scanActivity(s.pool.QueryRow(ctx, `
		INSERT INTO activities (section_id, kind, title, body, position, pass_percent, max_attempts, shuffle_options)
		VALUES ($1, $2, $3, $4, (SELECT COALESCE(MAX(position), -1) + 1 FROM activities WHERE section_id = $1),
		        COALESCE($5, 50), $6, COALESCE($7, TRUE))
		RETURNING `+activityCols,
		sectionID, in.Kind, in.Title, in.Body, in.PassPercent, in.MaxAttempts, in.ShuffleOptions))
}

// ActivityCourse resolves an activity's course for authorization.
func (s *Service) ActivityCourse(ctx context.Context, activityID uuid.UUID) (Course, error) {
	c, err := scanCourse(s.pool.QueryRow(ctx, `
		SELECT `+prefixedCourseCols("c")+`
		  FROM courses c
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id
		 WHERE a.id = $1`, activityID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Course{}, httperror.NotFound("activity")
	}
	return c, err
}

// UpdateActivityInput carries optional activity fields; nil = unchanged.
// ClearMaxAttempts switches a quiz back to unlimited attempts.
type UpdateActivityInput struct {
	Title            *string
	Body             *string
	Position         *int
	PassPercent      *int
	MaxAttempts      *int
	ClearMaxAttempts bool
	ShuffleOptions   *bool
}

// UpdateActivity edits a page or a quiz's settings.
func (s *Service) UpdateActivity(ctx context.Context, id uuid.UUID, in UpdateActivityInput) (Activity, error) {
	if in.Title != nil {
		*in.Title = strings.TrimSpace(*in.Title)
		if *in.Title == "" {
			return Activity{}, httperror.Validation("title must not be empty", nil)
		}
	}
	if err := validateQuizSettings(in.PassPercent, in.MaxAttempts); err != nil {
		return Activity{}, err
	}
	a, err := scanActivity(s.pool.QueryRow(ctx, `
		UPDATE activities
		   SET title = COALESCE($2, title), body = COALESCE($3, body),
		       position = COALESCE($4, position),
		       pass_percent = COALESCE($5, pass_percent),
		       max_attempts = CASE WHEN $7 THEN NULL ELSE COALESCE($6, max_attempts) END,
		       shuffle_options = COALESCE($8, shuffle_options),
		       updated_at = now()
		 WHERE id = $1
		 RETURNING `+activityCols,
		id, in.Title, in.Body, in.Position, in.PassPercent, in.MaxAttempts, in.ClearMaxAttempts, in.ShuffleOptions))
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, httperror.NotFound("activity")
	}
	return a, err
}

// DeleteActivity removes a page and (via cascade) its completions.
func (s *Service) DeleteActivity(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM activities WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("activity")
	}
	return err
}

func prefixedCourseCols(alias string) string {
	cols := strings.Split(courseCols, ", ")
	for i, c := range cols {
		cols[i] = alias + "." + c
	}
	return strings.Join(cols, ", ")
}
