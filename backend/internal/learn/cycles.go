package learn

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// dbtx is what both the pool and a transaction offer, so helpers can run
// inside or outside a transaction.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// cycleOf is the SQL for a person's current cycle on a course: their
// enrolment's cycle, or 1 without an enrolment (staff previewing a course).
// Completions and quiz attempts only count in the current cycle.
func cycleOf(courseExpr, userExpr string) string {
	return `COALESCE((SELECT e.cycle FROM enrolments e WHERE e.course_id = ` + courseExpr +
		` AND e.user_id = ` + userExpr + `), 1)`
}

// activityCycleOf is cycleOf for the course an activity belongs to.
func activityCycleOf(activityExpr, userExpr string) string {
	return `COALESCE((SELECT e.cycle FROM enrolments e
	                    JOIN sections cs ON cs.course_id = e.course_id
	                    JOIN activities ca ON ca.section_id = cs.id
	                   WHERE ca.id = ` + activityExpr + ` AND e.user_id = ` + userExpr + `), 1)`
}

// activityCycle returns a person's current cycle on an activity's course.
func activityCycle(ctx context.Context, q dbtx, activityID, userID uuid.UUID) (int, error) {
	var cycle int
	err := q.QueryRow(ctx, `SELECT `+activityCycleOf("$1", "$2"), activityID, userID).Scan(&cycle)
	return cycle, err
}

// completeActivity records a completion in the person's current cycle and
// issues the course's certificate when that finished the course (and the
// certificate of any learning path that finished with it).
func completeActivity(ctx context.Context, tx dbtx, activityID, userID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO completions (activity_id, user_id, cycle)
		VALUES ($1, $2, `+activityCycleOf("$1", "$2")+`)
		ON CONFLICT (activity_id, user_id, cycle) DO NOTHING`, activityID, userID); err != nil {
		return err
	}
	var courseID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT s.course_id FROM activities a JOIN sections s ON s.id = a.section_id WHERE a.id = $1`,
		activityID).Scan(&courseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = awardIfComplete(ctx, tx, courseID, userID); err != nil {
		return err
	}
	return awardPathsIfComplete(ctx, tx, courseID, userID)
}

// uncompleteActivity removes a completion from the current cycle only;
// earlier cycles are history. Certificates already issued stay.
func uncompleteActivity(ctx context.Context, tx dbtx, activityID, userID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		DELETE FROM completions
		 WHERE activity_id = $1 AND user_id = $2 AND cycle = `+activityCycleOf("$1", "$2"),
		activityID, userID)
	return err
}

// completedInCurrentCycle reports whether the person has completed the
// activity in their current cycle.
func completedInCurrentCycle(ctx context.Context, q dbtx, activityID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM completions
		                WHERE activity_id = $1 AND user_id = $2 AND cycle = `+activityCycleOf("$1", "$2")+`)`,
		activityID, userID).Scan(&ok)
	return ok, err
}
