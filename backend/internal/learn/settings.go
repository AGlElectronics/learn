package learn

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AGlElectronics/learn/backend/internal/db"
	"github.com/AGlElectronics/learn/backend/internal/denlink"
	"github.com/AGlElectronics/learn/backend/internal/httperror"
)

// Module setting keys (table module_settings).
const (
	settingTelegramEnabled = "telegram_enabled"
	settingRetention       = "retention"
)

// Den's module settings Learn reads (docs/concepts/module-settings.md).
// A key Den sets wins over Learn's own value and is shown as managed in Den.
const (
	DenKeyInactiveUsersDays = "retention.inactive_users_days"
	DenKeyAuditLogDays      = "learn.audit_log_days"
	DenKeyTelegramEnabled   = "learn.telegram_enabled"
)

// RetentionSettings say how long Learn keeps personal data, in days. 0 means
// never delete or anonymise automatically.
type RetentionSettings struct {
	// InactiveUsersDays: people who have not signed in (nor been synced)
	// for this many days are anonymised. Their certificates keep
	// verifying, with the name replaced. Module setting
	// retention.inactive_users_days.
	InactiveUsersDays int `json:"inactive_users_days"`
	// AuditLogDays: audit entries older than this are deleted. Module
	// setting learn.audit_log_days.
	AuditLogDays int `json:"audit_log_days"`
}

// DefaultRetention applies until an admin (or Den) sets retention.
// PENDING LEGAL REVIEW: these defaults have not been checked against the
// organisation's legal retention duties yet; set your own.
var DefaultRetention = RetentionSettings{InactiveUsersDays: 1095, AuditLogDays: 730}

// maxRetentionDays bounds the settings (100 years).
const maxRetentionDays = 36500

func validRetentionDays(v int) bool { return v >= 0 && v <= maxRetentionDays }

// Validate rejects negative or absurd values.
func (r RetentionSettings) Validate() error {
	for name, v := range map[string]int{"inactive_users_days": r.InactiveUsersDays, "audit_log_days": r.AuditLogDays} {
		if !validRetentionDays(v) {
			return httperror.Validation(name+" must be between 0 (never) and 36500", nil)
		}
	}
	return nil
}

// RetentionPatch changes some retention settings; nil fields stay.
type RetentionPatch struct {
	InactiveUsersDays *int `json:"inactive_users_days"`
	AuditLogDays      *int `json:"audit_log_days"`
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
	// Retention is Learn's own retention (used for every key Den does not
	// set).
	Retention RetentionSettings `json:"retention"`
	// RetentionSet is false while Learn's own retention is the default.
	RetentionSet bool `json:"-"`
}

// SetModuleSettings sets where Den's module settings come from, normally
// the Den link's ModuleSettings method (nil while standalone). Set it before
// serving requests.
func (s *Service) SetModuleSettings(settings func() *denlink.ModuleSettings) {
	s.denSettings = settings
}

// den returns Den's module settings for Learn, nil while standalone. The
// result's methods are safe on nil.
func (s *Service) den() *denlink.ModuleSettings {
	if s.denSettings == nil {
		return nil
	}
	return s.denSettings()
}

// GetModuleSettings reads Learn's own settings, with defaults for unset
// keys: Telegram on (it still needs TELEGRAM_BOT_TOKEN), retention
// DefaultRetention.
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
			r := DefaultRetention
			if json.Unmarshal(raw, &r) == nil && r.Validate() == nil {
				out.Retention, out.RetentionSet = r, true
			}
		}
	}
	return out, rows.Err()
}

// DenTelegramEnabled is Den's learn.telegram_enabled; ok is false when Den
// does not set it.
func (s *Service) DenTelegramEnabled() (enabled, ok bool) {
	return s.den().Bool(DenKeyTelegramEnabled)
}

// TelegramEnabled reports whether Telegram delivery is on: Den's
// learn.telegram_enabled when set, else Learn's own setting (true when
// unset or unreadable, so a database hiccup does not silently drop
// messages).
func (s *Service) TelegramEnabled(ctx context.Context) bool {
	if on, ok := s.DenTelegramEnabled(); ok {
		return on
	}
	var on bool
	err := s.pool.QueryRow(ctx, `SELECT value FROM module_settings WHERE key = $1`, settingTelegramEnabled).Scan(&on)
	if errors.Is(err, pgx.ErrNoRows) || err != nil {
		return true
	}
	return on
}

// SaveModuleSettings changes the given settings (nil = unchanged). A key
// Den sets cannot be changed here (409).
func (s *Service) SaveModuleSettings(ctx context.Context, by uuid.UUID, telegram *bool, retention *RetentionPatch) error {
	managed := s.DenManaged()
	if telegram != nil && managed[DenKeyTelegramEnabled] {
		return httperror.Conflict(DenKeyTelegramEnabled + " is set in Den (Admin → Modules → Learn → Module settings) while Learn is paired")
	}
	var next RetentionSettings
	if retention != nil {
		for key, changed := range map[string]bool{
			DenKeyInactiveUsersDays: retention.InactiveUsersDays != nil,
			DenKeyAuditLogDays:      retention.AuditLogDays != nil,
		} {
			if changed && managed[key] {
				return httperror.Conflict(key + " is set in Den (Admin → Modules → Learn → Module settings) while Learn is paired")
			}
		}
		own, err := s.GetModuleSettings(ctx)
		if err != nil {
			return err
		}
		next = own.Retention
		if retention.InactiveUsersDays != nil {
			next.InactiveUsersDays = *retention.InactiveUsersDays
		}
		if retention.AuditLogDays != nil {
			next.AuditLogDays = *retention.AuditLogDays
		}
		if err := next.Validate(); err != nil {
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
			return set(settingRetention, next)
		}
		return nil
	})
}

// DenManaged lists the module settings keys Den currently sets (with a
// valid value) for Learn.
func (s *Service) DenManaged() map[string]bool {
	ms := s.den()
	out := map[string]bool{}
	for _, key := range []string{DenKeyInactiveUsersDays, DenKeyAuditLogDays} {
		if d, ok := ms.Days(key); ok && validRetentionDays(d) {
			out[key] = true
		}
	}
	if _, ok := ms.Bool(DenKeyTelegramEnabled); ok {
		out[DenKeyTelegramEnabled] = true
	}
	return out
}

// EffectiveRetention is the retention in force, key by key: Den's value
// when paired and Den sets it, else Learn's own, else the default. source
// is den when Den sets any key, learn when an admin saved Learn's own
// values, else default.
func (s *Service) EffectiveRetention(ctx context.Context) (RetentionSettings, string, error) {
	ms, err := s.GetModuleSettings(ctx)
	if err != nil {
		return DefaultRetention, RetentionFromDefault, err
	}
	out, source := ms.Retention, RetentionFromDefault
	if ms.RetentionSet {
		source = RetentionFromLearn
	}
	den := s.den()
	for key, dst := range map[string]*int{
		DenKeyInactiveUsersDays: &out.InactiveUsersDays,
		DenKeyAuditLogDays:      &out.AuditLogDays,
	} {
		if d, ok := den.Days(key); ok && validRetentionDays(d) {
			*dst, source = d, RetentionFromDen
		}
	}
	return out, source, nil
}
