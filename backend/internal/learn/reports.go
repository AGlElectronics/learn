package learn

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Completion report statuses.
const (
	ReportDone       = "done"
	ReportInProgress = "in_progress"
	ReportNotStarted = "not_started"
	ReportOverdue    = "overdue"
)

// Qualification report statuses. "expiring" = valid but expires within
// ExpiringSoonWindow; "missing" = should hold it (member of a group linked
// to a course or path awarding it) but has no valid certificate.
const (
	QualReportValid    = "valid"
	QualReportExpiring = "expiring"
	QualReportExpired  = "expired"
	QualReportMissing  = "missing"
)

// ReportScope is what one caller may report on. AllOrgs are the orgs where
// they hold report.view (every course and qualification there); StaffOrgs
// are orgs where course staffing counts (course.teach), so their own
// courses show in the completion report; ManageOrgs (course.manage) see
// every course's completion there.
type ReportScope struct {
	UserID     uuid.UUID
	AllOrgs    []uuid.UUID
	ManageOrgs []uuid.UUID
	StaffOrgs  []uuid.UUID
}

// courseScopeSQL limits courses (alias c) to the scope; $1 all orgs,
// $2 manage orgs, $3 staff orgs, $4 user id.
const courseScopeSQL = `(c.organization_id = ANY($1) OR c.organization_id = ANY($2)
	OR (c.organization_id = ANY($3) AND EXISTS (
	     SELECT 1 FROM enrolments t WHERE t.course_id = c.id AND t.user_id = $4
	        AND t.role = 'trainer' AND t.status = 'active')))`

// ReportCourse is a course the caller may report on (filter picker).
type ReportCourse struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Published bool      `json:"published"`
}

// ReportCourses lists the courses in the caller's completion scope.
func (s *Service) ReportCourses(ctx context.Context, sc ReportScope) ([]ReportCourse, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.title, c.published FROM courses c
		 WHERE `+courseScopeSQL+`
		 ORDER BY c.title, c.id`, sc.AllOrgs, sc.ManageOrgs, sc.StaffOrgs, sc.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportCourse{}
	for rows.Next() {
		var c ReportCourse
		if err := rows.Scan(&c.ID, &c.Title, &c.Published); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CompletionFilter narrows the completion report; zero values = no filter.
type CompletionFilter struct {
	CourseID *uuid.UUID
	Group    string
	Status   string
}

// CompletionRow is one active learner enrolment with its standing in the
// current cycle.
type CompletionRow struct {
	CourseID       uuid.UUID  `json:"course_id"`
	CourseTitle    string     `json:"course_title"`
	UserID         uuid.UUID  `json:"user_id"`
	DisplayName    string     `json:"display_name"`
	Email          string     `json:"email"`
	Source         string     `json:"source"`
	EnrolledAt     time.Time  `json:"enrolled_at"`
	DueAt          *time.Time `json:"due_at"`
	Completed      int        `json:"completed"`
	Total          int        `json:"total"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	Status         string     `json:"status"`
}

func completionStatus(done, total int, dueAt *time.Time, now time.Time) string {
	switch {
	case total > 0 && done >= total:
		return ReportDone
	case dueAt != nil && dueAt.Before(now):
		return ReportOverdue
	case done > 0:
		return ReportInProgress
	default:
		return ReportNotStarted
	}
}

// CompletionReport lists active learner enrolments of the courses in scope.
func (s *Service) CompletionReport(ctx context.Context, sc ReportScope, f CompletionFilter) ([]CompletionRow, error) {
	switch f.Status {
	case "", ReportDone, ReportInProgress, ReportNotStarted, ReportOverdue:
	default:
		return nil, httperror.Validation("status must be done, in_progress, not_started or overdue", nil)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.title, u.id, u.display_name, COALESCE(u.email, ''), e.source, e.created_at, e.due_at,
		       (SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = c.id),
		       (SELECT count(*) FROM completions cp
		          JOIN activities a ON a.id = cp.activity_id JOIN sections s ON s.id = a.section_id
		         WHERE s.course_id = c.id AND cp.user_id = e.user_id AND cp.cycle = e.cycle),
		       (SELECT max(cp.completed_at) FROM completions cp
		          JOIN activities a ON a.id = cp.activity_id JOIN sections s ON s.id = a.section_id
		         WHERE s.course_id = c.id AND cp.user_id = e.user_id AND cp.cycle = e.cycle)
		  FROM enrolments e
		  JOIN courses c ON c.id = e.course_id
		  JOIN users u ON u.id = e.user_id
		 WHERE e.status = 'active' AND e.role = 'learner'
		   AND `+courseScopeSQL+`
		   AND ($5::uuid IS NULL OR c.id = $5)
		   AND ($6 = '' OR EXISTS (SELECT 1 FROM user_groups ug WHERE ug.user_id = e.user_id AND ug.group_name = $6))
		 ORDER BY c.title, c.id, u.display_name, u.id`,
		sc.AllOrgs, sc.ManageOrgs, sc.StaffOrgs, sc.UserID, f.CourseID, f.Group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []CompletionRow{}
	for rows.Next() {
		var r CompletionRow
		if err := rows.Scan(&r.CourseID, &r.CourseTitle, &r.UserID, &r.DisplayName, &r.Email, &r.Source,
			&r.EnrolledAt, &r.DueAt, &r.Total, &r.Completed, &r.LastActivityAt); err != nil {
			return nil, err
		}
		r.Status = completionStatus(r.Completed, r.Total, r.DueAt, now)
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QualificationFilter narrows the qualification report.
type QualificationFilter struct {
	QualificationID *uuid.UUID
	Group           string
	Status          string
}

// QualificationRow is one person and one qualification: their best
// (unrevoked, latest-expiring) certificate, if any. Required = they are in
// a group linked to a course or path that awards it.
type QualificationRow struct {
	QualificationID   uuid.UUID  `json:"qualification_id"`
	QualificationKey  string     `json:"qualification_key"`
	QualificationName string     `json:"qualification_name"`
	UserID            uuid.UUID  `json:"user_id"`
	DisplayName       string     `json:"display_name"`
	Email             string     `json:"email"`
	Required          bool       `json:"required"`
	CertificateID     *uuid.UUID `json:"certificate_id"`
	IssuedAt          *time.Time `json:"issued_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Status            string     `json:"status"`
}

func qualificationStatus(certID *uuid.UUID, expiresAt *time.Time, now time.Time) string {
	switch {
	case certID == nil:
		return QualReportMissing
	case expiresAt == nil:
		return QualReportValid
	case !now.Before(*expiresAt):
		return QualReportExpired
	case expiresAt.Sub(now) <= ExpiringSoonWindow:
		return QualReportExpiring
	default:
		return QualReportValid
	}
}

// QualificationReport lists, per qualification of the orgs in scope,
// everyone who holds or held it plus everyone who should hold it.
func (s *Service) QualificationReport(ctx context.Context, orgIDs []uuid.UUID, f QualificationFilter) ([]QualificationRow, error) {
	switch f.Status {
	case "", QualReportValid, QualReportExpiring, QualReportExpired, QualReportMissing:
	default:
		return nil, httperror.Validation("status must be valid, expiring, expired or missing", nil)
	}
	rows, err := s.pool.Query(ctx, `
		WITH q AS (
			SELECT id, key, name FROM qualifications
			 WHERE organization_id = ANY($1) AND ($2::uuid IS NULL OR id = $2)
		), req AS (
			SELECT q.id AS qid, ug.user_id
			  FROM q JOIN courses c ON c.awards_qualification_id = q.id
			  JOIN course_group_links l ON l.course_id = c.id
			  JOIN user_groups ug ON ug.group_name = l.group_name
			UNION
			SELECT q.id, ug.user_id
			  FROM q JOIN paths p ON p.awards_qualification_id = q.id
			  JOIN path_group_links l ON l.path_id = p.id
			  JOIN user_groups ug ON ug.group_name = l.group_name
		), pop AS (
			SELECT qid, user_id FROM req
			UNION
			SELECT ct.qualification_id, ct.user_id FROM certificates ct JOIN q ON q.id = ct.qualification_id
		)
		SELECT q.id, q.key, q.name, u.id, u.display_name, COALESCE(u.email, ''),
		       EXISTS (SELECT 1 FROM req r WHERE r.qid = pop.qid AND r.user_id = pop.user_id),
		       best.id, best.issued_at, best.expires_at
		  FROM pop
		  JOIN q ON q.id = pop.qid
		  JOIN users u ON u.id = pop.user_id
		  LEFT JOIN LATERAL (
			SELECT ct.id, ct.issued_at, ct.expires_at FROM certificates ct
			 WHERE ct.qualification_id = pop.qid AND ct.user_id = pop.user_id AND ct.revoked_at IS NULL
			 ORDER BY ct.expires_at DESC NULLS FIRST, ct.issued_at DESC
			 LIMIT 1) best ON TRUE
		 WHERE ($3 = '' OR EXISTS (SELECT 1 FROM user_groups g WHERE g.user_id = u.id AND g.group_name = $3))
		 ORDER BY q.name, q.id, u.display_name, u.id`, orgIDs, f.QualificationID, f.Group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	now := time.Now()
	out := []QualificationRow{}
	for rows.Next() {
		var r QualificationRow
		if err := rows.Scan(&r.QualificationID, &r.QualificationKey, &r.QualificationName, &r.UserID,
			&r.DisplayName, &r.Email, &r.Required, &r.CertificateID, &r.IssuedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		r.Status = qualificationStatus(r.CertificateID, r.ExpiresAt, now)
		if f.Status != "" && r.Status != f.Status {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
