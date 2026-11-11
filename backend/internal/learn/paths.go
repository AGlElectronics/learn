package learn

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Path is a learning path: an ordered list of courses, managed with
// course.manage in its organization.
type Path struct {
	ID                    uuid.UUID         `json:"id"`
	OrganizationID        uuid.UUID         `json:"organization_id"`
	Name                  string            `json:"name"`
	Description           string            `json:"description"`
	Sequential            bool              `json:"sequential"`
	SelfEnrol             bool              `json:"self_enrol"`
	AwardsQualificationID *uuid.UUID        `json:"awards_qualification_id"`
	AwardsQualification   *QualificationRef `json:"awards_qualification"`
	CreatedAt             time.Time         `json:"created_at"`
	UpdatedAt             time.Time         `json:"updated_at"`
	Courses               []PathCourse      `json:"courses"`
	// ActiveEnrolments counts active path enrolments.
	ActiveEnrolments int `json:"active_enrolments"`
}

// PathCourse is one course of a path, in order.
type PathCourse struct {
	CourseID  uuid.UUID `json:"course_id"`
	Title     string    `json:"title"`
	Published bool      `json:"published"`
	Position  int       `json:"position"`
}

// PathEnrolment is one person on one path.
type PathEnrolment struct {
	ID           uuid.UUID  `json:"id"`
	PathID       uuid.UUID  `json:"path_id"`
	UserID       uuid.UUID  `json:"user_id"`
	DisplayName  string     `json:"display_name"`
	Email        string     `json:"email"`
	Source       string     `json:"source"`
	Status       string     `json:"status"`
	LeftGroupAt  *time.Time `json:"left_group_at"`
	Cycle        int        `json:"cycle"`
	CreatedAt    time.Time  `json:"created_at"`
	CoursesDone  int        `json:"courses_done"`
	CoursesTotal int        `json:"courses_total"`
	Complete     bool       `json:"complete"`
}

// PathGroupLink is one Keycloak group linked to a path; GroupName and
// GroupPath as on GroupLink.
type PathGroupLink struct {
	PathID        uuid.UUID  `json:"path_id"`
	GroupName     string     `json:"group_name"`
	GroupPath     string     `json:"group_path"`
	PathResolved  bool       `json:"path_resolved"`
	CreatedAt     time.Time  `json:"created_at"`
	LastSyncedAt  *time.Time `json:"last_synced_at"`
	LastSyncError string     `json:"last_sync_error"`
	Members       int        `json:"members"`
	Active        int        `json:"active"`
}

// MyPathCourse is a course of a path as its learner sees it.
type MyPathCourse struct {
	PathCourse
	Progress Progress `json:"progress"`
	Complete bool     `json:"complete"`
	Locked   bool     `json:"locked"`
	Needs    []string `json:"needs,omitempty"`
}

// MyPath is a path the caller is enrolled in, with progress.
type MyPath struct {
	ID                  uuid.UUID         `json:"id"`
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Sequential          bool              `json:"sequential"`
	Source              string            `json:"source"`
	AwardsQualification *QualificationRef `json:"awards_qualification"`
	Courses             []MyPathCourse    `json:"courses"`
	CoursesDone         int               `json:"courses_done"`
	CoursesTotal        int               `json:"courses_total"`
	Complete            bool              `json:"complete"`
	CertificateID       *uuid.UUID        `json:"certificate_id"`
}

// pathCoveredSQL: the person has an active path enrolment on a path that
// contains the course. Group enrolments covered this way stay active.
func pathCoveredSQL(courseExpr, userExpr string) string {
	return `EXISTS (SELECT 1 FROM path_enrolments pe JOIN path_courses pc ON pc.path_id = pe.path_id
	                 WHERE pc.course_id = ` + courseExpr + ` AND pe.user_id = ` + userExpr + ` AND pe.status = 'active')`
}

// incompleteCourseSQL: the course has an activity the person hasn't
// completed in their current cycle (a course without activities counts as
// complete).
func incompleteCourseSQL(courseExpr, userExpr string) string {
	return `EXISTS (SELECT 1 FROM sections ics JOIN activities ia ON ia.section_id = ics.id
	                LEFT JOIN completions icp ON icp.activity_id = ia.id AND icp.user_id = ` + userExpr + `
	                     AND icp.cycle = ` + cycleOf("ics.course_id", userExpr) + `
	               WHERE ics.course_id = ` + courseExpr + ` AND icp.id IS NULL)`
}

const pathCols = `p.id, p.organization_id, p.name, p.description, p.sequential, p.self_enrol, p.awards_qualification_id,
	p.created_at, p.updated_at, ql.id, ql.key, ql.name, ql.valid_months,
	(SELECT count(*) FROM path_enrolments pe WHERE pe.path_id = p.id AND pe.status = 'active')`

const pathFrom = ` FROM paths p LEFT JOIN qualifications ql ON ql.id = p.awards_qualification_id`

func scanPath(row pgx.Row) (Path, error) {
	var p Path
	var qID *uuid.UUID
	var qKey, qName *string
	var qMonths *int
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.Sequential, &p.SelfEnrol,
		&p.AwardsQualificationID, &p.CreatedAt, &p.UpdatedAt, &qID, &qKey, &qName, &qMonths, &p.ActiveEnrolments)
	if qID != nil {
		p.AwardsQualification = &QualificationRef{ID: *qID, Key: *qKey, Name: *qName, ValidMonths: qMonths}
	}
	p.Courses = []PathCourse{}
	return p, err
}

func (s *Service) attachPathCourses(ctx context.Context, paths []Path) error {
	if len(paths) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(paths))
	index := map[uuid.UUID]int{}
	for i, p := range paths {
		ids[i] = p.ID
		index[p.ID] = i
	}
	rows, err := s.pool.Query(ctx, `
		SELECT pc.path_id, c.id, c.title, c.published, pc.position
		  FROM path_courses pc JOIN courses c ON c.id = pc.course_id
		 WHERE pc.path_id = ANY($1)
		 ORDER BY pc.path_id, pc.position, c.title`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var pid uuid.UUID
		var c PathCourse
		if err := rows.Scan(&pid, &c.CourseID, &c.Title, &c.Published, &c.Position); err != nil {
			return err
		}
		paths[index[pid]].Courses = append(paths[index[pid]].Courses, c)
	}
	return rows.Err()
}

// ListPaths returns the paths of the given organizations, by name.
func (s *Service) ListPaths(ctx context.Context, orgIDs []uuid.UUID) ([]Path, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+pathCols+pathFrom+` WHERE p.organization_id = ANY($1) ORDER BY p.name, p.id`, orgIDs)
	if err != nil {
		return nil, err
	}
	out := []Path{}
	for rows.Next() {
		p, err := scanPath(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachPathCourses(ctx, out)
}

// GetPath loads one path with its courses, or a typed 404.
func (s *Service) GetPath(ctx context.Context, id uuid.UUID) (Path, error) {
	p, err := scanPath(s.pool.QueryRow(ctx, `SELECT `+pathCols+pathFrom+` WHERE p.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Path{}, httperror.NotFound("path")
	}
	if err != nil {
		return Path{}, err
	}
	list := []Path{p}
	if err := s.attachPathCourses(ctx, list); err != nil {
		return Path{}, err
	}
	return list[0], nil
}

// PathInput carries path fields; nil = unchanged (on update).
// ClearQualification removes the awarded qualification.
type PathInput struct {
	Name                  *string
	Description           *string
	Sequential            *bool
	SelfEnrol             *bool
	AwardsQualificationID *uuid.UUID
	ClearQualification    bool
}

func (s *Service) checkPathQualification(ctx context.Context, orgID uuid.UUID, qualID *uuid.UUID) error {
	if qualID == nil {
		return nil
	}
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM qualifications WHERE id = $1 AND organization_id = $2)`,
		*qualID, orgID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return httperror.Validation("the qualification must belong to the same organization", nil)
	}
	return nil
}

// CreatePath adds a path to an organization.
func (s *Service) CreatePath(ctx context.Context, orgID, userID uuid.UUID, in PathInput) (Path, error) {
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return Path{}, httperror.Validation("name is required", nil)
	}
	if err := s.checkPathQualification(ctx, orgID, in.AwardsQualificationID); err != nil {
		return Path{}, err
	}
	desc := ""
	if in.Description != nil {
		desc = strings.TrimSpace(*in.Description)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO paths (organization_id, name, description, sequential, self_enrol, awards_qualification_id, created_by)
		VALUES ($1, $2, $3, COALESCE($4, FALSE), COALESCE($5, FALSE), $6, $7)
		RETURNING id`, orgID, name, desc, in.Sequential, in.SelfEnrol, in.AwardsQualificationID, userID).Scan(&id)
	if err != nil {
		return Path{}, err
	}
	return s.GetPath(ctx, id)
}

// UpdatePath patches a path.
func (s *Service) UpdatePath(ctx context.Context, path Path, in PathInput) (Path, error) {
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		if *in.Name == "" {
			return Path{}, httperror.Validation("name must not be empty", nil)
		}
	}
	if in.Description != nil {
		*in.Description = strings.TrimSpace(*in.Description)
	}
	if err := s.checkPathQualification(ctx, path.OrganizationID, in.AwardsQualificationID); err != nil {
		return Path{}, err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE paths
		   SET name = COALESCE($2, name), description = COALESCE($3, description),
		       sequential = COALESCE($4, sequential), self_enrol = COALESCE($5, self_enrol),
		       awards_qualification_id = CASE WHEN $7 THEN NULL ELSE COALESCE($6, awards_qualification_id) END,
		       updated_at = now()
		 WHERE id = $1`, path.ID, in.Name, in.Description, in.Sequential, in.SelfEnrol,
		in.AwardsQualificationID, in.ClearQualification); err != nil {
		return Path{}, err
	}
	if in.AwardsQualificationID != nil {
		// Members who already finished the path get the newly set qualification.
		err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT user_id FROM path_enrolments WHERE path_id = $1 AND status = 'active'`, path.ID)
			if err != nil {
				return err
			}
			users, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
			if err != nil {
				return err
			}
			for _, u := range users {
				if _, err := awardPathIfComplete(ctx, tx, path.ID, u); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return Path{}, err
		}
	}
	return s.GetPath(ctx, path.ID)
}

// DeletePath removes a path and its path enrolments and links. Course
// enrolments stay; group ones nothing else covers turn inactive.
// Certificates it awarded stay (path_id is cleared).
func (s *Service) DeletePath(ctx context.Context, id uuid.UUID) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var courseIDs []uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(course_id), '{}') FROM path_courses WHERE path_id = $1`, id).
			Scan(&courseIDs); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM paths WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httperror.NotFound("path")
		}
		_, err = deactivateUncovered(ctx, tx, nil, courseIDs)
		return err
	})
}

// SetPathCourses replaces the ordered course list of a path (add, remove
// and reorder in one). Every course must belong to the path's
// organization. Active path members are enrolled in added courses; group
// enrolments on removed courses that nothing else covers turn inactive.
func (s *Service) SetPathCourses(ctx context.Context, path Path, courseIDs []uuid.UUID) (Path, error) {
	seen := map[uuid.UUID]bool{}
	uniq := []uuid.UUID{}
	for _, id := range courseIDs {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "learn-path:"+path.ID.String()); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM courses WHERE id = ANY($1) AND organization_id = $2`,
			uniq, path.OrganizationID).Scan(&n); err != nil {
			return err
		}
		if n != len(uniq) {
			return httperror.Validation("every course must exist and belong to the path's organization", nil)
		}
		var removed []uuid.UUID
		if err := tx.QueryRow(ctx, `
			WITH d AS (DELETE FROM path_courses WHERE path_id = $1 AND NOT (course_id = ANY($2)) RETURNING course_id)
			SELECT COALESCE(array_agg(course_id), '{}') FROM d`, path.ID, uniq).Scan(&removed); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO path_courses (path_id, course_id, position)
			SELECT $1, c, ord - 1 FROM unnest($2::uuid[]) WITH ORDINALITY AS t(c, ord)
			ON CONFLICT (path_id, course_id) DO UPDATE SET position = EXCLUDED.position`, path.ID, uniq); err != nil {
			return err
		}
		if err := enrolPathCourses(ctx, tx, &path.ID, nil); err != nil {
			return err
		}
		if len(removed) > 0 {
			if _, err := deactivateUncovered(ctx, tx, nil, removed); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Path{}, err
	}
	return s.GetPath(ctx, path.ID)
}

// enrolPathCourses enrols active path members (optionally of one path
// and/or one person) in every course of their paths. The course enrolment
// takes the path enrolment's source (manual wins when both apply).
// Inactive enrolments are reactivated: a manual path enrolment
// reactivates any, a group one only group enrolments.
func enrolPathCourses(ctx context.Context, tx dbtx, pathID, userID *uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO enrolments (course_id, user_id, role, source)
		SELECT DISTINCT ON (pc.course_id, pe.user_id) pc.course_id, pe.user_id, 'learner', pe.source
		  FROM path_enrolments pe JOIN path_courses pc ON pc.path_id = pe.path_id
		 WHERE pe.status = 'active'
		   AND ($1::uuid IS NULL OR pe.path_id = $1)
		   AND ($2::uuid IS NULL OR pe.user_id = $2)
		 ORDER BY pc.course_id, pe.user_id, (pe.source = 'manual') DESC
		ON CONFLICT (course_id, user_id) DO UPDATE
		   SET status = 'active', left_group_at = NULL,
		       source = CASE WHEN EXCLUDED.source = 'manual' THEN 'manual' ELSE enrolments.source END
		 WHERE enrolments.status = 'inactive'
		   AND (enrolments.source = 'group' OR EXCLUDED.source = 'manual')`, pathID, userID)
	return err
}

// deactivateUncovered turns active group course enrolments inactive when
// neither a linked course group (as Learn knows membership) nor an active
// path enrolment still covers them. users/courses nil = no filter.
func deactivateUncovered(ctx context.Context, tx dbtx, users, courses []uuid.UUID) (int, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE enrolments e SET status = 'inactive', left_group_at = now()
		 WHERE e.source = 'group' AND e.status = 'active'
		   AND ($1::uuid[] IS NULL OR e.user_id = ANY($1))
		   AND ($2::uuid[] IS NULL OR e.course_id = ANY($2))
		   AND NOT `+pathCoveredSQL("e.course_id", "e.user_id")+`
		   AND NOT EXISTS (SELECT 1 FROM course_group_links l
		                     JOIN user_groups ug ON ug.group_name = l.group_name AND ug.user_id = e.user_id
		                    WHERE l.course_id = e.course_id)`, users, courses)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// ----------------------------------------------------------------------------
// Path enrolments
// ----------------------------------------------------------------------------

// ListPathEnrolments returns everyone on a path with course progress.
func (s *Service) ListPathEnrolments(ctx context.Context, pathID uuid.UUID) ([]PathEnrolment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pe.id, pe.path_id, pe.user_id, u.display_name, COALESCE(u.email, ''), pe.source, pe.status,
		       pe.left_group_at, pe.cycle, pe.created_at,
		       (SELECT count(*) FROM path_courses pc WHERE pc.path_id = pe.path_id),
		       (SELECT count(*) FROM path_courses pc WHERE pc.path_id = pe.path_id
		           AND NOT `+incompleteCourseSQL("pc.course_id", "pe.user_id")+`)
		  FROM path_enrolments pe JOIN users u ON u.id = pe.user_id
		 WHERE pe.path_id = $1
		 ORDER BY pe.status, u.display_name, u.id`, pathID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PathEnrolment{}
	for rows.Next() {
		var e PathEnrolment
		if err := rows.Scan(&e.ID, &e.PathID, &e.UserID, &e.DisplayName, &e.Email, &e.Source, &e.Status,
			&e.LeftGroupAt, &e.Cycle, &e.CreatedAt, &e.CoursesTotal, &e.CoursesDone); err != nil {
			return nil, err
		}
		e.Complete = e.CoursesTotal > 0 && e.CoursesDone == e.CoursesTotal
		out = append(out, e)
	}
	return out, rows.Err()
}

// EnrolInPath enrols a person in a path (manual) and in all its courses.
// An inactive path enrolment becomes an active manual one.
func (s *Service) EnrolInPath(ctx context.Context, pathID, userID uuid.UUID) (PathEnrolment, error) {
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO path_enrolments (path_id, user_id, source) VALUES ($1, $2, 'manual')
			ON CONFLICT (path_id, user_id) DO UPDATE
			   SET source = 'manual', status = 'active', left_group_at = NULL
			 WHERE path_enrolments.status = 'inactive'`, pathID, userID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return httperror.NotFound("user")
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httperror.Conflict("this person is already on the path")
		}
		if err := enrolPathCourses(ctx, tx, &pathID, &userID); err != nil {
			return err
		}
		_, err = awardPathIfComplete(ctx, tx, pathID, userID)
		return err
	})
	if err != nil {
		return PathEnrolment{}, err
	}
	return s.pathEnrolmentOf(ctx, pathID, userID)
}

// SelfEnrolPath enrols the caller on a self-enrol path.
func (s *Service) SelfEnrolPath(ctx context.Context, path Path, userID uuid.UUID) (PathEnrolment, error) {
	if !path.SelfEnrol {
		return PathEnrolment{}, httperror.New(httperror.CodeForbidden, "this path does not allow self-enrolment")
	}
	return s.EnrolInPath(ctx, path.ID, userID)
}

func (s *Service) pathEnrolmentOf(ctx context.Context, pathID, userID uuid.UUID) (PathEnrolment, error) {
	list, err := s.ListPathEnrolments(ctx, pathID)
	if err != nil {
		return PathEnrolment{}, err
	}
	for _, e := range list {
		if e.UserID == userID {
			return e, nil
		}
	}
	return PathEnrolment{}, httperror.NotFound("path enrolment")
}

// PathEnrolmentPath resolves a path enrolment's path for authorization.
func (s *Service) PathEnrolmentPath(ctx context.Context, enrolmentID uuid.UUID) (Path, error) {
	var pathID uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT path_id FROM path_enrolments WHERE id = $1`, enrolmentID).Scan(&pathID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Path{}, httperror.NotFound("path enrolment")
	}
	if err != nil {
		return Path{}, err
	}
	return s.GetPath(ctx, pathID)
}

// RemovePathEnrolment deletes a path enrolment by hand. Course enrolments
// stay; group ones nothing else covers turn inactive. Progress is kept.
func (s *Service) RemovePathEnrolment(ctx context.Context, enrolmentID uuid.UUID) error {
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var userID, pathID uuid.UUID
		err := tx.QueryRow(ctx, `DELETE FROM path_enrolments WHERE id = $1 RETURNING user_id, path_id`, enrolmentID).
			Scan(&userID, &pathID)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("path enrolment")
		}
		if err != nil {
			return err
		}
		var courseIDs []uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(course_id), '{}') FROM path_courses WHERE path_id = $1`, pathID).
			Scan(&courseIDs); err != nil {
			return err
		}
		_, err = deactivateUncovered(ctx, tx, []uuid.UUID{userID}, courseIDs)
		return err
	})
}

// ----------------------------------------------------------------------------
// Locks and completion
// ----------------------------------------------------------------------------

// courseLocks returns, for each of the given courses that is locked for
// the person, the titles of the earlier courses still to finish in their
// active sequential paths.
func courseLocks(ctx context.Context, q dbtx, userID uuid.UUID, courseIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	if len(courseIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT tgt.course_id, c.title
		  FROM path_enrolments pe
		  JOIN paths p ON p.id = pe.path_id AND p.sequential
		  JOIN path_courses tgt ON tgt.path_id = pe.path_id
		  JOIN path_courses prev ON prev.path_id = pe.path_id AND prev.position < tgt.position
		  JOIN courses c ON c.id = prev.course_id
		 WHERE pe.user_id = $1 AND pe.status = 'active' AND tgt.course_id = ANY($2)
		   AND `+incompleteCourseSQL("prev.course_id", "$1")+`
		 ORDER BY tgt.course_id, p.name, prev.position`, userID, courseIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		dup := false
		for _, t := range out[id] {
			dup = dup || t == title
		}
		if !dup {
			out[id] = append(out[id], title)
		}
	}
	return out, rows.Err()
}

// CourseLock reports the earlier path courses a learner must finish before
// this course opens (empty = not locked). Course staff are never locked.
func (s *Service) CourseLock(ctx context.Context, courseID, userID uuid.UUID) ([]string, error) {
	staff, err := s.IsStaff(ctx, courseID, userID)
	if err != nil || staff {
		return nil, err
	}
	locks, err := courseLocks(ctx, s.pool, userID, []uuid.UUID{courseID})
	if err != nil {
		return nil, err
	}
	return locks[courseID], nil
}

// courseLockedError is the 403 `locked` answer for a course whose earlier
// path courses aren't finished; details.needs names them.
func courseLockedError(needs []string) error {
	return httperror.New(httperror.CodeLocked, "finish "+strings.Join(needs, ", ")+" first").
		WithDetails(map[string]any{"needs": needs})
}

// RequireCourseUnlocked answers 403 `locked` when a sequential path still
// locks the course for the person.
func (s *Service) RequireCourseUnlocked(ctx context.Context, courseID, userID uuid.UUID) error {
	needs, err := s.CourseLock(ctx, courseID, userID)
	if err != nil {
		return err
	}
	if len(needs) > 0 {
		return courseLockedError(needs)
	}
	return nil
}

// attachCourseLocks marks learner course list entries locked by a path.
func (s *Service) attachCourseLocks(ctx context.Context, list []CourseSummary, userID uuid.UUID) error {
	ids := []uuid.UUID{}
	for _, c := range list {
		if c.EnrolmentRole == "learner" {
			ids = append(ids, c.ID)
		}
	}
	locks, err := courseLocks(ctx, s.pool, userID, ids)
	if err != nil {
		return err
	}
	for i := range list {
		if needs := locks[list[i].ID]; len(needs) > 0 && list[i].EnrolmentRole == "learner" {
			list[i].Locked = true
			list[i].Needs = needs
		}
	}
	return nil
}

// awardPathsIfComplete checks every path containing the course on which
// the person is active, awarding the path qualification when complete.
func awardPathsIfComplete(ctx context.Context, q dbtx, courseID, userID uuid.UUID) error {
	rows, err := q.Query(ctx, `
		SELECT pe.path_id FROM path_enrolments pe
		  JOIN path_courses pc ON pc.path_id = pe.path_id
		  JOIN paths p ON p.id = pe.path_id
		 WHERE pc.course_id = $1 AND pe.user_id = $2 AND pe.status = 'active'
		   AND p.awards_qualification_id IS NOT NULL`, courseID, userID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := awardPathIfComplete(ctx, q, id, userID); err != nil {
			return err
		}
	}
	return nil
}

// awardPathIfComplete issues the path's certificate when the person (an
// active path member) has finished every course of it, each in its current
// cycle. Idempotent per path enrolment cycle.
func awardPathIfComplete(ctx context.Context, q dbtx, pathID, userID uuid.UUID) (*uuid.UUID, error) {
	var qualID *uuid.UUID
	var validMonths *int
	var cycle int
	err := q.QueryRow(ctx, `
		SELECT p.awards_qualification_id, ql.valid_months, pe.cycle
		  FROM paths p
		  JOIN path_enrolments pe ON pe.path_id = p.id AND pe.user_id = $2 AND pe.status = 'active'
		  LEFT JOIN qualifications ql ON ql.id = p.awards_qualification_id
		 WHERE p.id = $1`, pathID, userID).Scan(&qualID, &validMonths, &cycle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil || qualID == nil {
		return nil, err
	}
	var complete bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM path_courses pc JOIN sections s ON s.course_id = pc.course_id
		                 JOIN activities a ON a.section_id = s.id WHERE pc.path_id = $1)
		   AND NOT EXISTS (SELECT 1 FROM path_courses pc WHERE pc.path_id = $1
		                      AND `+incompleteCourseSQL("pc.course_id", "$2")+`)`, pathID, userID).Scan(&complete); err != nil {
		return nil, err
	}
	if !complete {
		return nil, nil
	}
	for range 5 {
		code, err := newVerifyCode()
		if err != nil {
			return nil, err
		}
		var id uuid.UUID
		err = q.QueryRow(ctx, `
			INSERT INTO certificates (user_id, qualification_id, path_id, cycle, expires_at, verify_code)
			VALUES ($1, $2, $3, $4,
			        CASE WHEN $5::int IS NULL THEN NULL ELSE now() + make_interval(months => $5::int) END, $6)
			ON CONFLICT DO NOTHING
			RETURNING id`, userID, *qualID, pathID, cycle, validMonths, code).Scan(&id)
		if err == nil {
			return &id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		var exists bool
		if err := q.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM certificates WHERE path_id = $1 AND user_id = $2 AND cycle = $3)`,
			pathID, userID, cycle).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, nil
		}
	}
	return nil, errors.New("could not generate a unique verify code")
}

// MyPaths returns the caller's active paths with per-course progress and
// lock state, plus self-enrol paths they could join.
func (s *Service) MyPaths(ctx context.Context, userID uuid.UUID) ([]MyPath, []Path, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+pathCols+`, pe.source, pe.cycle,
		       (SELECT ct.id FROM certificates ct WHERE ct.path_id = p.id AND ct.user_id = $1 AND ct.cycle = pe.cycle)
		`+pathFrom+`
		  JOIN path_enrolments pe ON pe.path_id = p.id AND pe.user_id = $1 AND pe.status = 'active'
		 ORDER BY p.name, p.id`, userID)
	if err != nil {
		return nil, nil, err
	}
	var paths []Path
	var extra []MyPath
	for rows.Next() {
		var p Path
		var qID *uuid.UUID
		var qKey, qName *string
		var qMonths *int
		var mp MyPath
		var cycle int
		if err := rows.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Description, &p.Sequential, &p.SelfEnrol,
			&p.AwardsQualificationID, &p.CreatedAt, &p.UpdatedAt, &qID, &qKey, &qName, &qMonths, &p.ActiveEnrolments,
			&mp.Source, &cycle, &mp.CertificateID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if qID != nil {
			p.AwardsQualification = &QualificationRef{ID: *qID, Key: *qKey, Name: *qName, ValidMonths: qMonths}
		}
		p.Courses = []PathCourse{}
		paths = append(paths, p)
		extra = append(extra, mp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if err := s.attachPathCourses(ctx, paths); err != nil {
		return nil, nil, err
	}
	allCourses := []uuid.UUID{}
	for _, p := range paths {
		for _, c := range p.Courses {
			allCourses = append(allCourses, c.CourseID)
		}
	}
	locks, err := courseLocks(ctx, s.pool, userID, allCourses)
	if err != nil {
		return nil, nil, err
	}
	out := []MyPath{}
	for i, p := range paths {
		mp := extra[i]
		mp.ID, mp.Name, mp.Description, mp.Sequential = p.ID, p.Name, p.Description, p.Sequential
		mp.AwardsQualification = p.AwardsQualification
		mp.Courses = []MyPathCourse{}
		activities := 0
		for _, c := range p.Courses {
			prog, err := s.progressFor(ctx, c.CourseID, userID)
			if err != nil {
				return nil, nil, err
			}
			mc := MyPathCourse{PathCourse: c, Progress: prog, Complete: prog.Completed >= prog.Total}
			if needs := locks[c.CourseID]; len(needs) > 0 {
				mc.Locked, mc.Needs = true, needs
			}
			activities += prog.Total
			if mc.Complete {
				mp.CoursesDone++
			}
			mp.Courses = append(mp.Courses, mc)
		}
		mp.CoursesTotal = len(p.Courses)
		mp.Complete = mp.CoursesTotal > 0 && activities > 0 && mp.CoursesDone == mp.CoursesTotal
		out = append(out, mp)
	}

	rows, err = s.pool.Query(ctx, `SELECT `+pathCols+pathFrom+`
		 WHERE p.self_enrol AND NOT EXISTS (
		       SELECT 1 FROM path_enrolments pe WHERE pe.path_id = p.id AND pe.user_id = $1 AND pe.status = 'active')
		 ORDER BY p.name, p.id`, userID)
	if err != nil {
		return nil, nil, err
	}
	available := []Path{}
	for rows.Next() {
		p, err := scanPath(rows)
		if err != nil {
			rows.Close()
			return nil, nil, err
		}
		available = append(available, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, available, s.attachPathCourses(ctx, available)
}

// ----------------------------------------------------------------------------
// Path group links
// ----------------------------------------------------------------------------

// ListPathGroupLinks returns the groups linked to a path.
func (s *Service) ListPathGroupLinks(ctx context.Context, pathID uuid.UUID) ([]PathGroupLink, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.path_id, l.group_name, l.group_path, l.path_resolved, l.created_at, l.last_synced_at, l.last_sync_error,
		       (SELECT count(*) FROM user_groups ug WHERE ug.group_name = l.group_name),
		       (SELECT count(*) FROM path_enrolments pe
		          JOIN user_groups ug ON ug.user_id = pe.user_id AND ug.group_name = l.group_name
		         WHERE pe.path_id = l.path_id AND pe.source = 'group' AND pe.status = 'active')
		  FROM path_group_links l
		 WHERE l.path_id = $1
		 ORDER BY l.group_name`, pathID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PathGroupLink{}
	for rows.Next() {
		var l PathGroupLink
		if err := rows.Scan(&l.PathID, &l.GroupName, &l.GroupPath, &l.PathResolved, &l.CreatedAt, &l.LastSyncedAt, &l.LastSyncError,
			&l.Members, &l.Active); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// enrolPathGroupMembers enrols users (as $2 uuid[]) on a path as group
// members, reactivating inactive group path enrolments, then fans out to
// the path's courses.
func enrolPathGroupMembers(ctx context.Context, tx dbtx, pathID uuid.UUID, users []uuid.UUID) (enrolled, reactivated int, err error) {
	rows, err := tx.Query(ctx, `
		INSERT INTO path_enrolments (path_id, user_id, source)
		SELECT DISTINCT $1::uuid, u, 'group' FROM unnest($2::uuid[]) AS u
		ON CONFLICT (path_id, user_id) DO UPDATE SET status = 'active', left_group_at = NULL
		 WHERE path_enrolments.source = 'group' AND path_enrolments.status = 'inactive'
		RETURNING (xmax = 0)`, pathID, users)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var inserted bool
		if err := rows.Scan(&inserted); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if inserted {
			enrolled++
		} else {
			reactivated++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if err := enrolPathCourses(ctx, tx, &pathID, nil); err != nil {
		return 0, 0, err
	}
	for _, u := range users {
		if _, err := awardPathIfComplete(ctx, tx, pathID, u); err != nil {
			return 0, 0, err
		}
	}
	return enrolled, reactivated, nil
}

// deactivatePathMembers turns group path enrolments inactive for the users
// selected by whereSQL ($1 path id, $2 the extra arg), then deactivates
// their group course enrolments on the path's courses that nothing else
// covers. Returns how many path enrolments turned inactive.
func deactivatePathMembers(ctx context.Context, tx dbtx, pathID uuid.UUID, whereSQL string, arg any) (int, error) {
	var users []uuid.UUID
	if err := tx.QueryRow(ctx, `
		WITH d AS (
			UPDATE path_enrolments pe SET status = 'inactive', left_group_at = now()
			 WHERE pe.path_id = $1 AND pe.source = 'group' AND pe.status = 'active' AND `+whereSQL+`
			RETURNING pe.user_id)
		SELECT COALESCE(array_agg(user_id), '{}') FROM d`, pathID, arg).Scan(&users); err != nil {
		return 0, err
	}
	if len(users) == 0 {
		return 0, nil
	}
	var courseIDs []uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(course_id), '{}') FROM path_courses WHERE path_id = $1`, pathID).
		Scan(&courseIDs); err != nil {
		return 0, err
	}
	if _, err := deactivateUncovered(ctx, tx, users, courseIDs); err != nil {
		return 0, err
	}
	return len(users), nil
}

// AddPathGroupLink links a group to a path and enrols the members Learn
// already knows about. The handler runs a Keycloak sync afterwards when
// one is configured.
func (s *Service) AddPathGroupLink(ctx context.Context, pathID uuid.UUID, path string, createdBy uuid.UUID) (PathGroupLink, error) {
	group := NormalizeGroup(path)
	if group == "" {
		return PathGroupLink{}, httperror.Validation("group_path is required", nil)
	}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO path_group_links (path_id, group_name, group_path, path_resolved, created_by)
			VALUES ($1, $2, $3, TRUE, $4)`,
			pathID, group, displayPath(path), createdBy); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return httperror.Conflict("this group is already linked to the path")
			}
			return err
		}
		var users []uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(user_id), '{}') FROM user_groups WHERE group_name = $1`, group).
			Scan(&users); err != nil {
			return err
		}
		_, _, err := enrolPathGroupMembers(ctx, tx, pathID, users)
		return err
	})
	if err != nil {
		return PathGroupLink{}, err
	}
	return s.getPathGroupLink(ctx, pathID, group)
}

func (s *Service) getPathGroupLink(ctx context.Context, pathID uuid.UUID, group string) (PathGroupLink, error) {
	links, err := s.ListPathGroupLinks(ctx, pathID)
	if err != nil {
		return PathGroupLink{}, err
	}
	group = NormalizeGroup(group)
	for _, l := range links {
		if l.GroupName == group {
			return l, nil
		}
	}
	return PathGroupLink{}, httperror.NotFound("group link")
}

// RemovePathGroupLink unlinks a group. Group path members in no remaining
// linked group (as far as Learn knows) turn inactive; nothing is deleted.
func (s *Service) RemovePathGroupLink(ctx context.Context, pathID uuid.UUID, group string) (int, error) {
	group = NormalizeGroup(group)
	var n int
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM path_group_links WHERE path_id = $1 AND group_name = $2`, pathID, group)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httperror.NotFound("group link")
		}
		n, err = deactivatePathMembers(ctx, tx, pathID, `NOT EXISTS (
			SELECT 1 FROM path_group_links l JOIN user_groups ug ON ug.group_name = l.group_name AND ug.user_id = pe.user_id
			 WHERE l.path_id = pe.path_id AND l.group_name <> $2)`, group)
		return err
	})
	return n, err
}

// applyLoginPathGroups is the path half of ApplyLoginGroups: join linked
// paths (or reactivate), go inactive on paths whose groups the person
// left, and enrol them in the courses of their active paths.
func applyLoginPathGroups(ctx context.Context, tx dbtx, userID uuid.UUID, groups []string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO path_enrolments (path_id, user_id, source)
		SELECT DISTINCT l.path_id, $1::uuid, 'group' FROM path_group_links l WHERE l.group_name = ANY($2)
		ON CONFLICT (path_id, user_id) DO UPDATE SET status = 'active', left_group_at = NULL
		 WHERE path_enrolments.source = 'group' AND path_enrolments.status = 'inactive'`, userID, groups); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE path_enrolments pe SET status = 'inactive', left_group_at = now()
		 WHERE pe.user_id = $1 AND pe.source = 'group' AND pe.status = 'active'
		   AND NOT EXISTS (SELECT 1 FROM path_group_links l WHERE l.path_id = pe.path_id AND l.group_name = ANY($2))`,
		userID, groups); err != nil {
		return err
	}
	if err := enrolPathCourses(ctx, tx, nil, &userID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT path_id FROM path_enrolments WHERE user_id = $1 AND status = 'active'`, userID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := awardPathIfComplete(ctx, tx, id, userID); err != nil {
			return err
		}
	}
	return nil
}

// ----------------------------------------------------------------------------
// Keycloak sync for paths
// ----------------------------------------------------------------------------

// SyncPath syncs one path now ("Sync now").
func (g *GroupSync) SyncPath(ctx context.Context, pathID uuid.UUID) (GroupSyncResult, error) {
	return g.syncPath(ctx, pathID, map[string]memberLookup{})
}

// syncPathsAll syncs every path with linked groups (called from SyncAll
// under its lock).
func (g *GroupSync) syncPathsAll(ctx context.Context, cache map[string]memberLookup) (GroupSyncResult, error) {
	total := GroupSyncResult{Errors: []string{}}
	rows, err := g.svc.pool.Query(ctx, `SELECT DISTINCT path_id FROM path_group_links`)
	if err != nil {
		return total, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return total, err
	}
	for _, id := range ids {
		res, err := g.syncPath(ctx, id, cache)
		if err != nil {
			return total, err
		}
		total.add(res)
	}
	return total, nil
}

// syncPath enrols the members of every group linked to the path and makes
// group path enrolments of people in none of them inactive (with their
// uncovered group course enrolments). If any group could not be read,
// nobody is deactivated this round.
func (g *GroupSync) syncPath(ctx context.Context, pathID uuid.UUID, cache map[string]memberLookup) (GroupSyncResult, error) {
	res := GroupSyncResult{Errors: []string{}}
	rows, err := g.svc.pool.Query(ctx, `SELECT group_name, group_path FROM path_group_links WHERE path_id = $1 ORDER BY group_name`, pathID)
	if err != nil {
		return res, err
	}
	groups, err := collectLinkedGroups(rows)
	if err != nil {
		return res, err
	}
	complete := true
	perGroup := map[string][]uuid.UUID{}
	linkErr := map[string]string{}
	all := map[uuid.UUID]bool{}
	for _, group := range groups {
		m := g.members(ctx, group, cache)
		if m.err != nil {
			complete = false
			linkErr[group.key] = m.err.Error()
			res.Errors = append(res.Errors, group.path+": "+m.err.Error())
			continue
		}
		res.Groups++
		res.UsersCreated += m.created
		perGroup[group.key] = m.ids
		for _, id := range m.ids {
			all[id] = true
		}
	}
	memberIDs := make([]uuid.UUID, 0, len(all))
	for id := range all {
		memberIDs = append(memberIDs, id)
	}
	res.Members = len(memberIDs)

	err = db.WithTx(ctx, g.svc.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "learn-path:"+pathID.String()); err != nil {
			return err
		}
		if err := refreshGroupCache(ctx, tx, perGroup); err != nil {
			return err
		}
		var err error
		res.Enrolled, res.Reactivated, err = enrolPathGroupMembers(ctx, tx, pathID, memberIDs)
		if err != nil {
			return err
		}
		if complete {
			res.Deactivated, err = deactivatePathMembers(ctx, tx, pathID, `NOT (pe.user_id = ANY($2))`, memberIDs)
			if err != nil {
				return err
			}
		}
		for _, group := range groups {
			if _, err := tx.Exec(ctx, `
				UPDATE path_group_links SET last_synced_at = now(), last_sync_error = $3
				 WHERE path_id = $1 AND group_name = $2`, pathID, group.key, linkErr[group.key]); err != nil {
				return err
			}
		}
		return nil
	})
	return res, err
}
