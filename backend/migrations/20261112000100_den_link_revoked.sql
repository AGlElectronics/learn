-- Learn — Den can reject the module credential.
--
-- Shared denlink (con-services shared/go/denlink, SchemaUpgrade): when Den
-- answers 401 to the stored credential, the link is marked revoked until an
-- admin pairs again. Without this column every pairing query fails.

-- +goose Up
ALTER TABLE den_link ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE den_link DROP COLUMN IF EXISTS revoked_at;
