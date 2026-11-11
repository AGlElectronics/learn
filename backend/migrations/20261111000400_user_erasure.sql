-- Learn — erased (anonymised) people.
--
-- Erasure keeps the users row so enrolments, attempts, attendance and
-- certificates stay consistent, but replaces everything that identifies the
-- person: display name, email, IdP subject and IdP ids. erased_at marks the
-- row so it never appears in pickers again and the retention job skips it.

-- +goose Up
ALTER TABLE users ADD COLUMN erased_at TIMESTAMPTZ;

CREATE INDEX users_last_login_idx ON users (COALESCE(last_login_at, created_at)) WHERE erased_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS users_last_login_idx;
ALTER TABLE users DROP COLUMN IF EXISTS erased_at;
