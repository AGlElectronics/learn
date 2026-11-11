package learn

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/AGlElectronics/learn/backend/internal/audit"
	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// ErasedDisplayName replaces an erased person's name everywhere it shows,
// including the public certificate check.
const ErasedDisplayName = "Former learner"

// ErasedActorName replaces an erased person's name in the audit log.
const ErasedActorName = "Erased user"

// EraseResult is what an erasure did.
type EraseResult struct {
	UserID   uuid.UUID `json:"user_id"`
	ErasedAt time.Time `json:"erased_at"`
}

// EraseUser anonymises one person. The users row stays so enrolments,
// attempts, attendance and certificates remain consistent, but the name,
// email, IdP subject, IdP ids and language go, as do their group cache,
// notification channels, organization memberships and sent-reminder log.
// Their feedback loses its author. Certificates keep verifying, showing
// ErasedDisplayName. A later sign-in with the same account creates a new,
// empty person. Erasing twice answers 409.
func (s *Service) EraseUser(ctx context.Context, userID uuid.UUID, actor audit.Actor) (EraseResult, error) {
	res := EraseResult{UserID: userID}
	err := db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var erased *time.Time
		err := tx.QueryRow(ctx, `SELECT erased_at FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&erased)
		if errors.Is(err, pgx.ErrNoRows) {
			return httperror.NotFound("user")
		}
		if err != nil {
			return err
		}
		if erased != nil {
			return httperror.Conflict("this person was already erased")
		}
		if err := tx.QueryRow(ctx, `
			UPDATE users
			   SET display_name = $2, email = NULL, subject = 'erased:' || id::text,
			       idp_user_id = NULL, idp_username = NULL, language = NULL,
			       erased_at = now(), updated_at = now()
			 WHERE id = $1
			 RETURNING erased_at`, userID, ErasedDisplayName).Scan(&res.ErasedAt); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM user_groups WHERE user_id = $1`,
			`DELETE FROM user_channels WHERE user_id = $1`,
			`DELETE FROM user_organizations WHERE user_id = $1`,
			`DELETE FROM reminders_sent WHERE user_id = $1`,
			`UPDATE feedback SET user_id = NULL WHERE user_id = $1`,
		} {
			if _, err := tx.Exec(ctx, q, userID); err != nil {
				return err
			}
		}
		if err := audit.ForgetActor(ctx, tx, userID, ErasedActorName); err != nil {
			return err
		}
		return audit.Write(ctx, tx, actor, audit.Entry{Action: audit.UserErased, TargetType: "user", TargetID: userID.String()})
	})
	return res, err
}

// UserOrganizations lists the organizations a (not erased) person belongs to.
func (s *Service) UserOrganizations(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT organization_id FROM user_organizations WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// RetentionResult counts one retention run.
type RetentionResult struct {
	Anonymised   int    `json:"anonymised"`
	AuditDeleted int64  `json:"audit_deleted"`
	Source       string `json:"source"`
}

// retentionBatch bounds how many people one run anonymises.
const retentionBatch = 500

// ApplyRetention anonymises people inactive longer than the retention
// (no sign-in and no group sync in that time) and deletes audit entries
// older than the audit retention. now is injectable for tests.
func (s *Service) ApplyRetention(ctx context.Context, log *audit.Log, now time.Time) (RetentionResult, error) {
	r, source, err := s.EffectiveRetention(ctx)
	res := RetentionResult{Source: source}
	if err != nil {
		return res, err
	}
	if r.InactiveUsersDays > 0 {
		cutoff := now.AddDate(0, 0, -r.InactiveUsersDays)
		rows, err := s.pool.Query(ctx, `
			SELECT u.id FROM users u
			 WHERE u.erased_at IS NULL
			   AND COALESCE(u.last_login_at, u.created_at) < $1
			   AND NOT EXISTS (SELECT 1 FROM user_groups ug WHERE ug.user_id = u.id AND ug.synced_at >= $1)
			 ORDER BY u.created_at
			 LIMIT $2`, cutoff, retentionBatch)
		if err != nil {
			return res, err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return res, err
		}
		for _, id := range ids {
			if _, err := s.EraseUser(ctx, id, audit.System); err != nil {
				return res, err
			}
			res.Anonymised++
		}
	}
	if r.AuditLogDays > 0 && log != nil {
		n, err := log.Purge(ctx, now.AddDate(0, 0, -r.AuditLogDays))
		if err != nil {
			return res, err
		}
		res.AuditDeleted = n
	}
	if res.Anonymised > 0 || res.AuditDeleted > 0 {
		log.Record(ctx, audit.System, audit.Entry{Action: audit.UsersAnonymised, TargetType: "retention",
			Details: map[string]any{"anonymised": res.Anonymised, "audit_deleted": res.AuditDeleted, "source": source,
				"inactive_users_days": r.InactiveUsersDays, "audit_log_days": r.AuditLogDays}})
	}
	return res, nil
}

// RunRetention applies retention shortly after start and every six hours
// until ctx ends, on one replica at a time.
func (s *Service) RunRetention(ctx context.Context, log *audit.Log, logger zerolog.Logger) {
	l := logger.With().Str("component", "retention").Logger()
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var res RetentionResult
			_, err := withJobLock(ctx, s.pool, "learn-retention", func() (int, error) {
				var err error
				res, err = s.ApplyRetention(ctx, log, time.Now())
				return res.Anonymised, err
			})
			switch {
			case err != nil:
				l.Error().Err(err).Msg("retention run failed")
			case res.Anonymised > 0 || res.AuditDeleted > 0:
				l.Info().Int("anonymised", res.Anonymised).Int64("audit_deleted", res.AuditDeleted).
					Str("source", res.Source).Msg("retention applied")
			}
			t.Reset(6 * time.Hour)
		}
	}
}
