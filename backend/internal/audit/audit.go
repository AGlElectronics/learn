// Package audit records who did what in Learn: enrolments and course roles,
// publishing and archiving, quiz grading and resets, certificate revokes,
// group links, session sign-up status, Den pairing, settings and erasure.
//
// Entries are append-only. Writing one never fails the action it records:
// a failed write is logged at error level instead, so an audit outage does
// not lock people out of their work.
package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// Actions. Keep in sync with the audit section of the API reference.
const (
	EnrolmentAdded     = "enrolment.added"
	EnrolmentRemoved   = "enrolment.removed"
	EnrolmentSelf      = "enrolment.self"
	EnrolmentUpdated   = "enrolment.updated"
	PathEnrolmentAdded = "path_enrolment.added"
	PathEnrolmentGone  = "path_enrolment.removed"
	CoursePublished    = "course.published"
	CourseUnpublished  = "course.unpublished"
	CourseArchived     = "course.archived"
	CourseUnarchived   = "course.unarchived"
	QuizGraded         = "quiz.graded"
	QuizReset          = "quiz.reset"
	CertificateRevoked = "certificate.revoked"
	QualificationSaved = "qualification.saved"
	QualificationGone  = "qualification.deleted"
	GroupLinkAdded     = "group_link.added"
	GroupLinkRemoved   = "group_link.removed"
	SignupStatus       = "session.signup_status"
	DenPaired          = "den.paired"
	DenSynced          = "den.synced"
	DenUnpaired        = "den.unpaired"
	SettingsUpdated    = "settings.updated"
	UserErased         = "user.erased"
	UsersAnonymised    = "users.anonymised"
)

// Entry is one action to record. Target is what the action changed (an
// enrolment, a course, a certificate, …).
type Entry struct {
	Action         string
	TargetType     string
	TargetID       string
	OrganizationID *uuid.UUID
	CourseID       *uuid.UUID
	Details        map[string]any
}

// Actor is who did it. A nil UserID is the system (a worker).
type Actor struct {
	UserID *uuid.UUID
	Name   string
}

// System is the actor for background jobs.
var System = Actor{Name: "system"}

// Record is one stored entry.
type Record struct {
	ID             int64          `json:"id"`
	At             time.Time      `json:"at"`
	ActorUserID    *uuid.UUID     `json:"actor_user_id"`
	ActorName      string         `json:"actor_name"`
	Action         string         `json:"action"`
	TargetType     string         `json:"target_type"`
	TargetID       string         `json:"target_id"`
	OrganizationID *uuid.UUID     `json:"organization_id"`
	CourseID       *uuid.UUID     `json:"course_id"`
	Details        map[string]any `json:"details"`
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Log writes and reads the audit_log table. A nil *Log records nothing.
type Log struct {
	pool *pgxpool.Pool
	log  zerolog.Logger
}

// New returns a Log over pool.
func New(pool *pgxpool.Pool, logger zerolog.Logger) *Log {
	return &Log{pool: pool, log: logger.With().Str("component", "audit").Logger()}
}

// Record writes one entry. Errors are logged, never returned.
func (l *Log) Record(ctx context.Context, actor Actor, e Entry) {
	if l == nil {
		return
	}
	if err := Write(ctx, l.pool, actor, e); err != nil {
		l.log.Error().Err(err).Str("action", e.Action).Str("target_id", e.TargetID).Msg("audit write failed")
	}
}

// Write inserts one entry with q (a pool or a transaction), for callers
// that want the entry to commit or roll back with their change.
func Write(ctx context.Context, q execer, actor Actor, e Entry) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, actor_name, action, target_type, target_id, organization_id, course_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		actor.UserID, actor.Name, e.Action, e.TargetType, e.TargetID, e.OrganizationID, e.CourseID, raw)
	return err
}

// Filter narrows List. Zero values mean "any".
type Filter struct {
	Action     string
	TargetType string
	TargetID   string
	CourseID   *uuid.UUID
	ActorID    *uuid.UUID
	// Organizations limits the entries to these organizations (plus
	// entries without one, such as Den pairing). Required.
	Organizations []uuid.UUID
	// BeforeID pages backwards: only entries with a smaller id.
	BeforeID int64
	Limit    int
}

// List returns entries newest first, and the BeforeID of the next page
// (0 when there is none).
func (l *Log) List(ctx context.Context, f Filter) ([]Record, int64, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	rows, err := l.pool.Query(ctx, `
		SELECT id, at, actor_user_id, actor_name, action, target_type, target_id, organization_id, course_id, details
		  FROM audit_log
		 WHERE (organization_id IS NULL OR organization_id = ANY($1))
		   AND ($2 = '' OR action = $2)
		   AND ($3 = '' OR target_type = $3)
		   AND ($4 = '' OR target_id = $4)
		   AND ($5::uuid IS NULL OR course_id = $5)
		   AND ($6::uuid IS NULL OR actor_user_id = $6)
		   AND ($7 = 0 OR id < $7)
		 ORDER BY id DESC
		 LIMIT $8`,
		f.Organizations, f.Action, f.TargetType, f.TargetID, f.CourseID, f.ActorID, f.BeforeID, f.Limit+1)
	if err != nil {
		return nil, 0, err
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Record, error) {
		var r Record
		var raw []byte
		if err := row.Scan(&r.ID, &r.At, &r.ActorUserID, &r.ActorName, &r.Action, &r.TargetType, &r.TargetID,
			&r.OrganizationID, &r.CourseID, &raw); err != nil {
			return r, err
		}
		r.Details = map[string]any{}
		_ = json.Unmarshal(raw, &r.Details)
		return r, nil
	})
	if err != nil {
		return nil, 0, err
	}
	var next int64
	if len(out) > f.Limit {
		out = out[:f.Limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

// Purge deletes entries older than before and returns how many went.
func (l *Log) Purge(ctx context.Context, before time.Time) (int64, error) {
	tag, err := l.pool.Exec(ctx, `DELETE FROM audit_log WHERE at < $1`, before)
	return tag.RowsAffected(), err
}

// ForgetActor replaces an erased user's name in the entries they made; the
// actions stay. Details only ever hold ids, never names or emails, so
// nothing else in the log identifies the person once their row is erased.
func ForgetActor(ctx context.Context, q execer, userID uuid.UUID, replacement string) error {
	_, err := q.Exec(ctx, `UPDATE audit_log SET actor_name = $2 WHERE actor_user_id = $1`, userID, replacement)
	return err
}
