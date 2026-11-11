-- Learn — group links by full path, and IdP user ids apart from `sub`.
--
-- Group links (courses and learning paths) now name a group by its full
-- path ("/learn/cohort-a"). Every stored group value is kept in one
-- comparison form (trimmed, one leading "/" dropped, lower-case) in
-- group_name, the column every join uses; group_path keeps the path as the
-- directory spells it, for display. The token's groups claim goes through
-- the same normalisation, so links match when the claim carries full paths.
--
-- Links made before this change named a group by its plain name. They are
-- marked path_resolved = false; the backend looks up their path once in the
-- group directory at start-up (see GroupSync.ResolveLegacyLinks).
--
-- users.idp_user_id is the user's id in the group directory (Keycloak) and
-- idp_username their username there. Synced users are matched on it
-- instead of assuming that the directory id equals the token's `sub`.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION learn_group_key(g TEXT) RETURNS TEXT AS $$
    SELECT lower(regexp_replace(btrim(g), '^/', ''))
$$ LANGUAGE sql IMMUTABLE;
-- +goose StatementEnd

-- user_groups: normalise, dropping rows that collapse into one.
DELETE FROM user_groups a USING user_groups b
 WHERE a.user_id = b.user_id AND learn_group_key(a.group_name) = learn_group_key(b.group_name)
   AND a.group_name > b.group_name;
UPDATE user_groups SET group_name = learn_group_key(group_name) WHERE group_name <> learn_group_key(group_name);

-- team boards pick from the same group values.
DELETE FROM team_board_groups a USING team_board_groups b
 WHERE a.id > b.id
   AND learn_group_key(a.group_name) = learn_group_key(b.group_name)
   AND a.course_id IS NOT DISTINCT FROM b.course_id
   AND a.qualification_id IS NOT DISTINCT FROM b.qualification_id;
UPDATE team_board_groups SET group_name = learn_group_key(group_name) WHERE group_name <> learn_group_key(group_name);

ALTER TABLE course_group_links
    ADD COLUMN group_path    TEXT NOT NULL DEFAULT '',
    ADD COLUMN path_resolved BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE course_group_links
   SET group_path = CASE WHEN group_name LIKE '/%' THEN btrim(group_name) ELSE '/' || btrim(group_name) END,
       path_resolved = group_name LIKE '/%';
DELETE FROM course_group_links a USING course_group_links b
 WHERE a.course_id = b.course_id AND learn_group_key(a.group_name) = learn_group_key(b.group_name)
   AND a.group_name > b.group_name;
UPDATE course_group_links SET group_name = learn_group_key(group_name);
ALTER TABLE course_group_links ADD CONSTRAINT course_group_links_key_check CHECK (group_name = learn_group_key(group_name));

ALTER TABLE path_group_links
    ADD COLUMN group_path    TEXT NOT NULL DEFAULT '',
    ADD COLUMN path_resolved BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE path_group_links
   SET group_path = CASE WHEN group_name LIKE '/%' THEN btrim(group_name) ELSE '/' || btrim(group_name) END,
       path_resolved = group_name LIKE '/%';
DELETE FROM path_group_links a USING path_group_links b
 WHERE a.path_id = b.path_id AND learn_group_key(a.group_name) = learn_group_key(b.group_name)
   AND a.group_name > b.group_name;
UPDATE path_group_links SET group_name = learn_group_key(group_name);
ALTER TABLE path_group_links ADD CONSTRAINT path_group_links_key_check CHECK (group_name = learn_group_key(group_name));

ALTER TABLE users
    ADD COLUMN idp_user_id  TEXT,
    ADD COLUMN idp_username TEXT;
CREATE UNIQUE INDEX users_idp_user_idx ON users (issuer, idp_user_id) WHERE idp_user_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_idp_user_idx;
ALTER TABLE users DROP COLUMN IF EXISTS idp_username, DROP COLUMN IF EXISTS idp_user_id;
ALTER TABLE path_group_links DROP CONSTRAINT IF EXISTS path_group_links_key_check;
UPDATE path_group_links SET group_name = group_path WHERE group_path <> '';
ALTER TABLE path_group_links DROP COLUMN IF EXISTS path_resolved, DROP COLUMN IF EXISTS group_path;
ALTER TABLE course_group_links DROP CONSTRAINT IF EXISTS course_group_links_key_check;
UPDATE course_group_links SET group_name = group_path WHERE group_path <> '';
ALTER TABLE course_group_links DROP COLUMN IF EXISTS path_resolved, DROP COLUMN IF EXISTS group_path;
DROP FUNCTION IF EXISTS learn_group_key(TEXT);
