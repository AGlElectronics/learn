-- Learn — learning paths per role.
--
-- A path is an ordered list of courses ("New Stash crew"). Enrolling in a
-- path enrols the person in every course of it (course enrolments carry
-- the path enrolment's source). In a sequential path a course stays
-- locked until the earlier ones are complete. Finishing every course can
-- award a qualification: the certificate then has path_id set and
-- course_id NULL, idempotent per path enrolment cycle.
--
-- Group links reuse the Keycloak group sync: members are enrolled, and
-- people who leave every linked group get an inactive path enrolment (and
-- their group course enrolments turn inactive unless something else still
-- covers them). Nothing is deleted automatically.

-- +goose Up
CREATE TABLE paths (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name                    TEXT NOT NULL CHECK (name <> ''),
    description             TEXT NOT NULL DEFAULT '',
    sequential              BOOLEAN NOT NULL DEFAULT FALSE,
    -- Signed-in people may enrol themselves.
    self_enrol              BOOLEAN NOT NULL DEFAULT FALSE,
    awards_qualification_id UUID REFERENCES qualifications(id) ON DELETE SET NULL,
    created_by              UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX paths_org_idx ON paths (organization_id);

CREATE TABLE path_courses (
    path_id   UUID NOT NULL REFERENCES paths(id) ON DELETE CASCADE,
    course_id UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    position  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (path_id, course_id)
);

CREATE INDEX path_courses_course_idx ON path_courses (course_id);

CREATE TABLE path_enrolments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    path_id       UUID NOT NULL REFERENCES paths(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source        TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'group')),
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    left_group_at TIMESTAMPTZ,
    cycle         INTEGER NOT NULL DEFAULT 1 CHECK (cycle > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (path_id, user_id)
);

CREATE INDEX path_enrolments_user_idx ON path_enrolments (user_id, status);

CREATE TABLE path_group_links (
    path_id         UUID NOT NULL REFERENCES paths(id) ON DELETE CASCADE,
    group_name      TEXT NOT NULL CHECK (group_name <> ''),
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_synced_at  TIMESTAMPTZ,
    last_sync_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (path_id, group_name)
);

CREATE INDEX path_group_links_group_idx ON path_group_links (group_name);

ALTER TABLE certificates ADD COLUMN path_id UUID REFERENCES paths(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX certificates_path_user_cycle_idx ON certificates (path_id, user_id, cycle) WHERE path_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS certificates_path_user_cycle_idx;
ALTER TABLE certificates DROP COLUMN IF EXISTS path_id;
DROP TABLE IF EXISTS path_group_links;
DROP TABLE IF EXISTS path_enrolments;
DROP TABLE IF EXISTS path_courses;
DROP TABLE IF EXISTS paths;
