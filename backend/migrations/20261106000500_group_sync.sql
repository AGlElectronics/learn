-- Learn — enrolment synced from Keycloak groups (cohorts).
--
-- Staff link Keycloak groups to a course; a worker (and "Sync now") enrols
-- every member as a learner. Nothing is deleted automatically: when someone
-- leaves every linked group their sync-created enrolment turns inactive
-- (access lost, progress kept) and rejoining reactivates it. Manual
-- enrolments are never touched by the sync.

-- +goose Up

-- Cache of who is in which Keycloak group, refreshed by the sync (for
-- synced members) and on every login from the token's groups claim.
CREATE TABLE user_groups (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_name TEXT NOT NULL,
    synced_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, group_name)
);

CREATE INDEX user_groups_group_idx ON user_groups (group_name);

-- group_name is the plain Keycloak group name, the same string the token's
-- groups claim carries.
CREATE TABLE course_group_links (
    course_id       UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    group_name      TEXT NOT NULL CHECK (group_name <> ''),
    role            TEXT NOT NULL DEFAULT 'learner' CHECK (role = 'learner'),
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_synced_at  TIMESTAMPTZ,
    last_sync_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (course_id, group_name)
);

CREATE INDEX course_group_links_group_idx ON course_group_links (group_name);

ALTER TABLE enrolments
    ADD COLUMN source        TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'group')),
    ADD COLUMN status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    ADD COLUMN left_group_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE enrolments
    DROP COLUMN IF EXISTS left_group_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS source;
DROP TABLE IF EXISTS course_group_links;
DROP TABLE IF EXISTS user_groups;
