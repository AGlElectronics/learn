-- Learn — module settings.
--
-- Settings an admin changes in Learn's Settings page (not environment
-- variables): whether Telegram delivery is on, and the retention periods
-- used while Learn is standalone. When Learn is paired with Den, Den's
-- module settings decide retention instead. One row per key.

-- +goose Up
CREATE TABLE module_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE IF EXISTS module_settings;
