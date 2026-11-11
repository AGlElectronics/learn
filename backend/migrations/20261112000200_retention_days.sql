-- Learn — retention in days, named like Den's module settings.
--
-- Learn's own retention (module_settings key "retention") was kept in
-- months: {"inactive_user_months": N, "audit_log_months": M}. It now uses
-- the day counts of the module settings contract
-- (docs/concepts/module-settings.md): retention.inactive_users_days and
-- learn.audit_log_days, stored as {"inactive_users_days": D,
-- "audit_log_days": E}. A month is converted as 365/12 days, rounded
-- (12 months = 365 days, 24 months = 730 days); 0 (never) stays 0. A month
-- key that was missing gets the old default (36 and 24 months).

-- +goose Up
UPDATE module_settings
   SET value = jsonb_build_object(
         'inactive_users_days', round(COALESCE((value->>'inactive_user_months')::numeric, 36) * 365 / 12)::int,
         'audit_log_days',      round(COALESCE((value->>'audit_log_months')::numeric, 24) * 365 / 12)::int),
       updated_at = now()
 WHERE key = 'retention' AND (value ? 'inactive_user_months' OR value ? 'audit_log_months');

-- +goose Down
UPDATE module_settings
   SET value = jsonb_build_object(
         'inactive_user_months', round(COALESCE((value->>'inactive_users_days')::numeric, 730) * 12 / 365)::int,
         'audit_log_months',     round(COALESCE((value->>'audit_log_days')::numeric, 730) * 12 / 365)::int),
       updated_at = now()
 WHERE key = 'retention' AND (value ? 'inactive_users_days' OR value ? 'audit_log_days');
