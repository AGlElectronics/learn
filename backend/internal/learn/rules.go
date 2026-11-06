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

// lockedError is the 403 `locked` answer for an activity whose
// prerequisites are not complete; details.needs names them.
func lockedError(needs []string) error {
	msg := "complete the earlier activities first"
	if len(needs) > 0 {
		msg = "complete " + strings.Join(needs, ", ") + " first"
	}
	return httperror.New(httperror.CodeLocked, msg).WithDetails(map[string]any{"needs": needs})
}

// ActivityLock reports whether an activity is locked for a user: some of
// its prerequisites aren't complete. needs lists their titles. Course staff
// (trainer enrolment) are never locked; callers with org-wide authority
// (course.manage) should skip the check, as the service can't see
// permissions.
func (s *Service) ActivityLock(ctx context.Context, activityID, userID uuid.UUID) (locked bool, needs []string, err error) {
	course, err := s.ActivityCourse(ctx, activityID)
	if err != nil {
		return false, nil, err
	}
	staff, err := s.IsStaff(ctx, course.ID, userID)
	if err != nil || staff {
		return false, nil, err
	}
	pathLocks, err := courseLocks(ctx, s.pool, userID, []uuid.UUID{course.ID})
	if err != nil {
		return false, nil, err
	}
	if needs := pathLocks[course.ID]; len(needs) > 0 {
		return true, needs, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.title
		  FROM activity_prerequisites p
		  JOIN activities a ON a.id = p.requires_activity_id
		  JOIN sections s ON s.id = a.section_id
		  LEFT JOIN completions c ON c.activity_id = a.id AND c.user_id = $2 AND c.cycle = `+cycleOf("s.course_id", "$2")+`
		 WHERE p.activity_id = $1 AND c.id IS NULL
		 ORDER BY s.position, a.position, a.title`, activityID, userID)
	if err != nil {
		return false, nil, err
	}
	defer rows.Close()
	needs = []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return false, nil, err
		}
		needs = append(needs, t)
	}
	if err := rows.Err(); err != nil {
		return false, nil, err
	}
	return len(needs) > 0, needs, nil
}

// RequireUnlocked returns the 403 `locked` error when the activity is
// locked for userID, nil otherwise.
func (s *Service) RequireUnlocked(ctx context.Context, activityID, userID uuid.UUID) error {
	locked, needs, err := s.ActivityLock(ctx, activityID, userID)
	if err != nil {
		return err
	}
	if locked {
		return lockedError(needs)
	}
	return nil
}

// SetPrerequisites replaces the activities that must be complete before
// activityID opens. They must belong to the same course and must not
// create a cycle.
func (s *Service) SetPrerequisites(ctx context.Context, activityID uuid.UUID, requires []uuid.UUID) ([]uuid.UUID, error) {
	course, err := s.ActivityCourse(ctx, activityID)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	uniq := []uuid.UUID{}
	for _, id := range requires {
		if id == activityID {
			return nil, httperror.Validation("an activity can't require itself", nil)
		}
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	err = db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"prerequisites:"+course.ID.String()); err != nil {
			return err
		}
		var sameCourse int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM activities a JOIN sections s ON s.id = a.section_id
			 WHERE a.id = ANY($1) AND s.course_id = $2`, uniq, course.ID).Scan(&sameCourse); err != nil {
			return err
		}
		if sameCourse != len(uniq) {
			return httperror.Validation("prerequisites must be activities of the same course", nil)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM activity_prerequisites WHERE activity_id = $1`, activityID); err != nil {
			return err
		}
		if len(uniq) == 0 {
			return nil
		}
		var cycle bool
		if err := tx.QueryRow(ctx, `
			WITH RECURSIVE reach(id) AS (
				SELECT unnest($1::uuid[])
				UNION
				SELECT p.requires_activity_id FROM activity_prerequisites p JOIN reach r ON p.activity_id = r.id
			)
			SELECT EXISTS (SELECT 1 FROM reach WHERE id = $2)`, uniq, activityID).Scan(&cycle); err != nil {
			return err
		}
		if cycle {
			return httperror.Validation("these prerequisites would create a loop (an activity would end up requiring itself)", nil)
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO activity_prerequisites (activity_id, requires_activity_id)
			SELECT $1, unnest($2::uuid[])`, activityID, uniq)
		return err
	})
	if err != nil {
		return nil, err
	}
	return uniq, nil
}

// AttachRules fills prerequisites, lock state and due dates on a course
// detail. For non-staff, locked activities lose their body.
func (s *Service) AttachRules(ctx context.Context, d *CourseDetail, userID uuid.UUID, staff bool) error {
	rows, err := s.pool.Query(ctx, `
		SELECT p.activity_id, p.requires_activity_id, r.title, (c.id IS NOT NULL)
		  FROM activity_prerequisites p
		  JOIN activities a ON a.id = p.activity_id
		  JOIN sections s ON s.id = a.section_id
		  JOIN activities r ON r.id = p.requires_activity_id
		  JOIN sections rs ON rs.id = r.section_id
		  LEFT JOIN completions c ON c.activity_id = r.id AND c.user_id = $2 AND c.cycle = `+cycleOf("$1", "$2")+`
		 WHERE s.course_id = $1
		 ORDER BY rs.position, r.position, r.title`, d.ID, userID)
	if err != nil {
		return err
	}
	type req struct {
		id    uuid.UUID
		title string
		done  bool
	}
	reqs := map[uuid.UUID][]req{}
	for rows.Next() {
		var aid uuid.UUID
		var r req
		if err := rows.Scan(&aid, &r.id, &r.title, &r.done); err != nil {
			rows.Close()
			return err
		}
		reqs[aid] = append(reqs[aid], r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !staff && d.EnrolmentRole == domain.EnrolmentTrainer {
		staff = true
	}
	for i := range d.Sections {
		for j := range d.Sections[i].Activities {
			a := &d.Sections[i].Activities[j]
			a.Prerequisites = []uuid.UUID{}
			for _, r := range reqs[a.ID] {
				a.Prerequisites = append(a.Prerequisites, r.id)
				if !r.done && !staff {
					a.Needs = append(a.Needs, r.title)
				}
			}
			if len(a.Needs) > 0 {
				a.Locked = true
				a.Body = ""
			}
		}
	}

	err = s.pool.QueryRow(ctx, `
		SELECT c.due_days, e.due_at
		  FROM courses c
		  LEFT JOIN enrolments e ON e.course_id = c.id AND e.user_id = $2 AND e.status = 'active'
		 WHERE c.id = $1`, d.ID, userID).Scan(&d.DueDays, &d.DueAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	d.Overdue = isOverdue(d.DueAt, d.Progress, time.Now())
	return nil
}

// isOverdue: past the due date with activities still to do.
func isOverdue(dueAt *time.Time, p Progress, now time.Time) bool {
	return dueAt != nil && dueAt.Before(now) && p.Completed < p.Total
}

// attachSummaryDue fills the caller's due date on course list entries.
func (s *Service) attachSummaryDue(ctx context.Context, list []CourseSummary, userID uuid.UUID) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(list))
	for i, c := range list {
		ids[i] = c.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT course_id, due_at FROM enrolments
		 WHERE user_id = $1 AND course_id = ANY($2) AND due_at IS NOT NULL AND status = 'active'`, userID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	due := map[uuid.UUID]time.Time{}
	for rows.Next() {
		var id uuid.UUID
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			return err
		}
		due[id] = t
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := time.Now()
	for i := range list {
		if t, ok := due[list[i].ID]; ok {
			list[i].DueAt = &t
			list[i].Overdue = isOverdue(&t, list[i].Progress, now)
		}
	}
	return nil
}

// SetCourseDueDays sets (or clears, with nil) how many days after
// enrolment learners are due. Existing enrolments keep their due date.
func (s *Service) SetCourseDueDays(ctx context.Context, courseID uuid.UUID, days *int) error {
	if days != nil && *days <= 0 {
		return httperror.Validation("due_days must be positive (or null for no due date)", nil)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE courses SET due_days = $2, updated_at = now() WHERE id = $1`, courseID, days)
	if err == nil && tag.RowsAffected() == 0 {
		return httperror.NotFound("course")
	}
	return err
}

// CourseDueDays returns a course's due_days setting.
func (s *Service) CourseDueDays(ctx context.Context, courseID uuid.UUID) (*int, error) {
	var days *int
	err := s.pool.QueryRow(ctx, `SELECT due_days FROM courses WHERE id = $1`, courseID).Scan(&days)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, httperror.NotFound("course")
	}
	return days, err
}

// SetEnrolmentDue overrides (or clears, with nil) one enrolment's due date.
func (s *Service) SetEnrolmentDue(ctx context.Context, enrolmentID uuid.UUID, dueAt *time.Time) (Enrolment, error) {
	var courseID uuid.UUID
	err := s.pool.QueryRow(ctx, `UPDATE enrolments SET due_at = $2 WHERE id = $1 RETURNING course_id`,
		enrolmentID, dueAt).Scan(&courseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Enrolment{}, httperror.NotFound("enrolment")
	}
	if err != nil {
		return Enrolment{}, err
	}
	list, err := s.ListEnrolments(ctx, courseID)
	if err != nil {
		return Enrolment{}, err
	}
	for _, e := range list {
		if e.ID == enrolmentID {
			return e, nil
		}
	}
	return Enrolment{}, httperror.NotFound("enrolment")
}
