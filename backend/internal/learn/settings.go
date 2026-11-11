package learn

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Module setting keys (table module_settings).
const (
	settingTelegramEnabled = "telegram_enabled"
	settingRetention       = "retention"
)

// RetentionSettings say how long Learn keeps personal data. 0 means never
// delete or anonymise automatically.
type RetentionSettings struct {
	// InactiveUserMonths: people who have not signed in (nor been synced)
	// for this many months are anonymised. Their certificates keep
	// verifying, with the name replaced.
	InactiveUserMonths int `json:"inactive_user_months"`
	// AuditLogMonths: audit entries older than this are deleted.
	AuditLogMonths int `json:"audit_log_months"`
}

// DefaultRetention applies until an admin (or Den) sets retention.
// PENDING LEGAL REVIEW: these defaults have not been checked against the
// organisation's legal retention duties yet; set your own.
var DefaultRetention = RetentionSettings{InactiveUserMonths: 36, AuditLogMonths: 24}

// maxRetentionMonths bounds the settings (100 years).
const maxRetentionMonths = 1200

// Validate rejects negative or absurd values.
func (r RetentionSettings) Validate() error {
	for name, v := range map[string]int{"inactive_user_months": r.InactiveUserMonths, "audit_log_months": r.AuditLogMonths} {
		if v < 0 || v > maxRetentionMonths {
			return httperror.Validation(name+" must be between 0 (never) and 1200", nil)
		}
	}
	return nil
}

// Retention sources.
const (
	RetentionFromDen     = "den"
	RetentionFromLearn   = "learn"
	RetentionFromDefault = "default"
)

// ModuleSettings are Learn's own settings.
type ModuleSettings struct {
	TelegramEnabled bool `json:"telegram_enabled"`
	// Retention is Learn's own retention (used while standalone).
	Retention RetentionSettings `json:"retention"`
	// RetentionSet is false while Learn's own retention is the default.
	RetentionSet bool `json:"-"`
}

// GetModuleSettings reads the settings, with defaults for unset keys:
// Telegram on (it still needs TELEGRAM_BOT_TOKEN), retention DefaultRetention.
func (s *Service) GetModuleSettings(ctx context.Context) (ModuleSettings, error) {
	out := ModuleSettings{TelegramEnabled: true, Retention: DefaultRetention}
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM module_settings`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return out, err
		}
		switch key {
		case settingTelegramEnabled:
			_ = json.Unmarshal(raw, &out.TelegramEnabled)
		case settingRetention:
			if json.Unmarshal(raw, &out.Retention) == nil {
				out.RetentionSet = true
			}
		}
	}
	return out, rows.Err()
}

// TelegramEnabled reports the telegram_enabled setting (true when unset or
// unreadable, so a database hiccup does not silently drop messages).
func (s *Service) TelegramEnabled(ctx context.Context) bool {
	var on bool
	err := s.pool.QueryRow(ctx, `SELECT value FROM module_settings WHERE key = $1`, settingTelegramEnabled).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return true
	}
	return on
}

// SaveModuleSettings changes the given settings (nil = unchanged).
func (s *Service) SaveModuleSettings(ctx context.Context, by uuid.UUID, telegram *bool, retention *RetentionSettings) error {
	if retention != nil {
		if err := retention.Validate(); err != nil {
			return err
		}
	}
	return db.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		set := func(key string, v any) error {
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO module_settings (key, value, updated_by) VALUES ($1, $2, $3)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
				key, raw, by)
			return err
		}
		if telegram != nil {
			if err := set(settingTelegramEnabled, *telegram); err != nil {
				return err
			}
		}
		if retention != nil {
			return set(settingRetention, *retention)
		}
		return nil
	})
}

// DenRetention reads the retention Den pushes in its integration bundle
// (settings.data.retention: {"inactive_user_months": N, "audit_log_months":
// M}); ok is false when Den sends none. A key Den leaves out falls back to
// DefaultRetention.
func DenRetention(settings map[string]any) (RetentionSettings, bool) {
	raw, ok := settings[settingRetention].(map[string]any)
	if !ok {
		return RetentionSettings{}, false
	}
	out := DefaultRetention
	if v, ok := raw["inactive_user_months"].(float64); ok {
		out.InactiveUserMonths = int(v)
	}
	if v, ok := raw["audit_log_months"].(float64); ok {
		out.AuditLogMonths = int(v)
	}
	if out.Validate() != nil {
		return RetentionSettings{}, false
	}
	return out, true
}

// DenSettings returns Den's module settings for Learn; ok is false when
// standalone. *denlink.Client's Bundle provides them.
type DenSettings func() (map[string]any, bool)

// EffectiveRetention is the retention in force: Den's when paired and Den
// sets it, else Learn's own, else the default. source names which.
func (s *Service) EffectiveRetention(ctx context.Context, den DenSettings) (RetentionSettings, string, error) {
	if den != nil {
		if data, ok := den(); ok {
			if r, ok := DenRetention(data); ok {
				return r, RetentionFromDen, nil
			}
		}
	}
	ms, err := s.GetModuleSettings(ctx)
	if err != nil {
		return DefaultRetention, RetentionFromDefault, err
	}
	if ms.RetentionSet {
		return ms.Retention, RetentionFromLearn, nil
	}
	return DefaultRetention, RetentionFromDefault, nil
}
