package learn

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Portal to-do severities (suite-wide contract, see Den's portal widget).
const (
	TodoInfo    = "info"
	TodoWarning = "warning"
	TodoDanger  = "danger"
)

const (
	todoCourseWindow  = 14 * 24 * time.Hour
	todoCourseWarning = 3 * 24 * time.Hour
	todoSessionWindow = 7 * 24 * time.Hour
)

// PortalTodo is one item of GET /api/portal/todos. Path is relative to the
// SPA; the HTTP layer turns it into the absolute URL.
type PortalTodo struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Detail   string     `json:"detail"`
	DueAt    *time.Time `json:"due_at"`
	URL      string     `json:"url"`
	Severity string     `json:"severity"`
	Path     string     `json:"-"`
}

var todoSeverityRank = map[string]int{TodoDanger: 0, TodoWarning: 1, TodoInfo: 2}

// SortPortalTodos orders by due date (none last), then severity.
func SortPortalTodos(items []PortalTodo) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch {
		case a.DueAt != nil && b.DueAt != nil && !a.DueAt.Equal(*b.DueAt):
			return a.DueAt.Before(*b.DueAt)
		case a.DueAt != nil && b.DueAt == nil:
			return true
		case a.DueAt == nil && b.DueAt != nil:
			return false
		}
		return todoSeverityRank[a.Severity] < todoSeverityRank[b.Severity]
	})
}

// PortalTodos collects the caller's to-dos: courses due within 14 days or
// overdue, booked sessions within 7 days, certificates expiring within 30
// days or expired with renewal possible, and failed quizzes with attempts
// left. Sorted by due date. Names are translated and texts worded for
// langs (English when none of them has to-do texts).
func (s *Service) PortalTodos(ctx context.Context, userID uuid.UUID, now time.Time, langs Langs) ([]PortalTodo, error) {
	out := []PortalTodo{}
	for _, collect := range []func(context.Context, uuid.UUID, time.Time, Langs) ([]PortalTodo, error){
		s.todoCourses, s.todoSessions, s.todoCertificates, s.todoQuizzes,
	} {
		items, err := collect(ctx, userID, now, langs)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	SortPortalTodos(out)
	return out, nil
}

func (s *Service) todoCourses(ctx context.Context, userID uuid.UUID, now time.Time, langs Langs) ([]PortalTodo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.title, e.due_at,
		       (SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id WHERE s.course_id = c.id),
		       (SELECT count(*) FROM completions cp
		          JOIN activities a ON a.id = cp.activity_id JOIN sections s ON s.id = a.section_id
		         WHERE s.course_id = c.id AND cp.user_id = e.user_id AND cp.cycle = e.cycle)
		  FROM enrolments e
		  JOIN courses c ON c.id = e.course_id
		 WHERE e.user_id = $1 AND e.role = 'learner' AND e.status = 'active' AND c.published
		   AND e.due_at IS NOT NULL AND e.due_at <= $2`, userID, now.Add(todoCourseWindow))
	if err != nil {
		return nil, err
	}
	type row struct {
		courseID    uuid.UUID
		title       string
		dueAt       time.Time
		total, done int
	}
	found := []*row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.courseID, &r.title, &r.dueAt, &r.total, &r.done); err != nil {
			rows.Close()
			return nil, err
		}
		if r.total > 0 && r.done < r.total {
			found = append(found, &r)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tr := s.translator(langs)
	for _, r := range found {
		tr.course(r.courseID, TrCourse, r.courseID, "title", &r.title)
	}
	if err := tr.apply(ctx); err != nil {
		return nil, err
	}
	out := []PortalTodo{}
	for _, r := range found {
		dueAt := r.dueAt
		t := PortalTodo{
			ID:     "learn:course:" + r.courseID.String(),
			Title:  todoText(langs, "finish", r.title),
			Detail: todoText(langs, "progress", r.done, r.total),
			DueAt:  &dueAt, Path: "/courses/" + r.courseID.String(),
		}
		switch {
		case !dueAt.After(now):
			t.Severity = TodoDanger
			t.Detail = todoText(langs, "overdue", t.Detail)
		case dueAt.Sub(now) <= todoCourseWarning:
			t.Severity = TodoWarning
		default:
			t.Severity = TodoInfo
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Service) todoSessions(ctx context.Context, userID uuid.UUID, now time.Time, langs Langs) ([]PortalTodo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sl.id, sl.starts_at, sl.location, a.id, a.title, c.id, c.title
		  FROM session_signups su
		  JOIN session_slots sl ON sl.id = su.slot_id
		  JOIN activities a ON a.id = su.activity_id
		  JOIN sections s ON s.id = a.section_id
		  JOIN courses c ON c.id = s.course_id
		  JOIN enrolments e ON e.course_id = c.id AND e.user_id = su.user_id AND e.status = 'active'
		 WHERE su.user_id = $1 AND su.status = 'booked' AND c.published
		   AND sl.starts_at > $2 AND sl.starts_at <= $3`, userID, now, now.Add(todoSessionWindow))
	if err != nil {
		return nil, err
	}
	type row struct {
		slotID, activityID, courseID uuid.UUID
		startsAt                     time.Time
		location, activity, course   string
	}
	found := []*row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.slotID, &r.startsAt, &r.location, &r.activityID, &r.activity, &r.courseID, &r.course); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, &r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tr := s.translator(langs)
	for _, r := range found {
		tr.course(r.courseID, TrActivity, r.activityID, "title", &r.activity)
		tr.course(r.courseID, TrCourse, r.courseID, "title", &r.course)
	}
	if err := tr.apply(ctx); err != nil {
		return nil, err
	}
	out := []PortalTodo{}
	for _, r := range found {
		detail := r.course
		if l := strings.TrimSpace(r.location); l != "" {
			detail += " · " + l
		}
		startsAt := r.startsAt
		out = append(out, PortalTodo{
			ID: "learn:session:" + r.slotID.String(), Title: r.activity, Detail: detail,
			DueAt: &startsAt, Severity: TodoInfo, Path: "/courses/" + r.courseID.String(),
		})
	}
	return out, nil
}

// todoCertificates: the latest unrevoked certificate per qualification.
// Expiring within 30 days is a warning (unless a renewal is already under
// way, which the course item covers); expired is danger when the person can
// still renew on the awarding course.
func (s *Service) todoCertificates(ctx context.Context, userID uuid.UUID, now time.Time, langs Langs) ([]PortalTodo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ct.id, ct.expires_at, ql.id, ql.name, c.id, COALESCE(c.title, ''), ct.cycle, e.cycle
		  FROM certificates ct
		  JOIN qualifications ql ON ql.id = ct.qualification_id
		  LEFT JOIN courses c ON c.id = ct.course_id AND c.published
		  LEFT JOIN enrolments e ON e.course_id = c.id AND e.user_id = ct.user_id
		                        AND e.role = 'learner' AND e.status = 'active'
		 WHERE ct.user_id = $1 AND ct.revoked_at IS NULL AND ct.expires_at IS NOT NULL
		   AND ct.expires_at <= $2
		   AND NOT EXISTS (SELECT 1 FROM certificates newer
		                    WHERE newer.user_id = ct.user_id AND newer.qualification_id = ct.qualification_id
		                      AND newer.issued_at > ct.issued_at AND newer.revoked_at IS NULL)`,
		userID, now.Add(ExpiringSoonWindow))
	if err != nil {
		return nil, err
	}
	type row struct {
		certID, qualID        uuid.UUID
		courseID              *uuid.UUID
		expiresAt             time.Time
		qualification, course string
		certCycle             int
		enrolCycle            *int
	}
	found := []*row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.certID, &r.expiresAt, &r.qualID, &r.qualification, &r.courseID, &r.course,
			&r.certCycle, &r.enrolCycle); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, &r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tr := s.translator(langs)
	for _, r := range found {
		tr.loose(TrQualification, r.qualID, "name", &r.qualification)
		if r.courseID != nil {
			tr.course(*r.courseID, TrCourse, *r.courseID, "title", &r.course)
		}
	}
	if err := tr.apply(ctx); err != nil {
		return nil, err
	}
	out := []PortalTodo{}
	for _, r := range found {
		renewing := r.enrolCycle != nil && *r.enrolCycle > r.certCycle
		canRenew := r.enrolCycle != nil && *r.enrolCycle == r.certCycle
		expiresAt := r.expiresAt
		t := PortalTodo{ID: "learn:certificate:" + r.certID.String(), DueAt: &expiresAt, Path: "/courses"}
		if !expiresAt.After(now) {
			if !canRenew {
				continue
			}
			t.Severity = TodoDanger
			t.Title = todoText(langs, "renew", r.qualification)
			t.Detail = todoText(langs, "expired_renew", r.course)
		} else {
			if renewing {
				continue
			}
			t.Severity = TodoWarning
			t.Title = todoText(langs, "expires_soon", r.qualification)
			if canRenew {
				t.Detail = todoText(langs, "renew_by", r.course)
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// todoQuizzes: quizzes on active courses whose best attempt this cycle
// failed and that still have attempts left.
func (s *Service) todoQuizzes(ctx context.Context, userID uuid.UUID, _ time.Time, langs Langs) ([]PortalTodo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.title, c.id, c.title, a.max_attempts, count(qa.id)
		  FROM enrolments e
		  JOIN courses c ON c.id = e.course_id
		  JOIN sections s ON s.course_id = c.id
		  JOIN activities a ON a.section_id = s.id AND a.kind = 'quiz'
		  JOIN quiz_attempts qa ON qa.activity_id = a.id AND qa.user_id = e.user_id
		                       AND qa.cycle = e.cycle AND qa.submitted_at IS NOT NULL
		 WHERE e.user_id = $1 AND e.role = 'learner' AND e.status = 'active' AND c.published
		 GROUP BY a.id, a.title, c.id, c.title, a.max_attempts
		HAVING NOT bool_or(COALESCE(qa.passed, false))
		   AND (a.max_attempts IS NULL OR count(qa.id) < a.max_attempts)`, userID)
	if err != nil {
		return nil, err
	}
	type row struct {
		activityID, courseID uuid.UUID
		title, course        string
		maxAttempts          *int
		used                 int
	}
	found := []*row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.activityID, &r.title, &r.courseID, &r.course, &r.maxAttempts, &r.used); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, &r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	tr := s.translator(langs)
	for _, r := range found {
		tr.course(r.courseID, TrActivity, r.activityID, "title", &r.title)
		tr.course(r.courseID, TrCourse, r.courseID, "title", &r.course)
	}
	if err := tr.apply(ctx); err != nil {
		return nil, err
	}
	out := []PortalTodo{}
	for _, r := range found {
		detail := todoText(langs, "not_passed", r.course)
		if r.maxAttempts != nil {
			if left := *r.maxAttempts - r.used; left == 1 {
				detail = todoText(langs, "attempt_left_1", detail)
			} else {
				detail = todoText(langs, "attempts_left", detail, left)
			}
		}
		out = append(out, PortalTodo{
			ID: "learn:quiz:" + r.activityID.String(), Title: todoText(langs, "retry", r.title), Detail: detail,
			Severity: TodoInfo, Path: "/courses/" + r.courseID.String(),
		})
	}
	return out, nil
}
