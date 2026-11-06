-- Learn — initial schema.
--
-- Scope: the whole proof-of-concept slice — organizations, users keyed by
-- the OIDC (issuer, subject) pair, courses made of ordered sections and
-- page activities, enrolments, per-activity completions, and the Den
-- pairing row. Quizzes, gradebooks and file activities are later
-- migrations; activities.kind is the extension point.

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- +goose StatementEnd

-- ----------------------------------------------------------------------------
-- ORGANIZATIONS
-- ----------------------------------------------------------------------------
-- Every course belongs to one organization. The slice ships a single seeded
-- row (demo-org, referenced by infra/access-config.toml); the boundary is in
-- the schema so multi-org deployments need no rework.

CREATE TABLE organizations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (slug = lower(slug) AND slug ~ '^[a-z0-9][a-z0-9-]*[a-z0-9]$')
);

-- ----------------------------------------------------------------------------
-- USERS
-- ----------------------------------------------------------------------------
-- The (issuer, subject) pair is the stable identity key (architecture.md
-- §4.4); "dev" is the reserved issuer for AUTH_MODE=dev. display_name and
-- email are refreshed on every login and MUST NOT be used as join keys.
-- Roles are NOT stored here: they come from IdP groups via the access
-- config on every request.

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    email         TEXT,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

CREATE INDEX users_email_lower_idx ON users (lower(email));

-- ----------------------------------------------------------------------------
-- COURSES / SECTIONS / ACTIVITIES
-- ----------------------------------------------------------------------------

CREATE TABLE courses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL DEFAULT '',
    -- Unpublished courses are invisible to everyone but the course staff.
    published       BOOLEAN NOT NULL DEFAULT FALSE,
    -- Published + self_enrol courses appear in the catalog, where any
    -- signed-in person may enrol themselves.
    self_enrol      BOOLEAN NOT NULL DEFAULT FALSE,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX courses_org_idx       ON courses (organization_id);
CREATE INDEX courses_published_idx ON courses (published, self_enrol);

CREATE TABLE sections (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id  UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    title      TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sections_course_idx ON sections (course_id, position);

CREATE TABLE activities (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    section_id UUID NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'page' CHECK (kind IN ('page')),
    title      TEXT NOT NULL,
    -- Page body, stored as text (no uploads in this slice).
    body       TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX activities_section_idx ON activities (section_id, position);

-- ----------------------------------------------------------------------------
-- ENROLMENTS & COMPLETIONS
-- ----------------------------------------------------------------------------
-- An enrolment is what lets a person read a published course; role "trainer"
-- additionally makes them course staff (edit content, manage enrolments,
-- given the matching permission). There is no "learner" IdP group.

CREATE TABLE enrolments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id  UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL DEFAULT 'learner' CHECK (role IN ('learner', 'trainer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (course_id, user_id)
);

CREATE INDEX enrolments_user_idx ON enrolments (user_id);

CREATE TABLE completions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    activity_id  UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    completed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (activity_id, user_id)
);

CREATE INDEX completions_user_idx ON completions (user_id);

-- ----------------------------------------------------------------------------
-- DEN PAIRING
-- ----------------------------------------------------------------------------
-- Pairing with Den (see internal/denlink). One row when paired, none when
-- standalone. Same table every Con Services module uses.

CREATE TABLE den_link (
    id            SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    den_base_url  TEXT NOT NULL,
    credential    TEXT NOT NULL,
    module_id     TEXT NOT NULL DEFAULT '',
    paired_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    synced_at     TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT '',
    bundle        JSONB
);

-- ----------------------------------------------------------------------------
-- SEED
-- ----------------------------------------------------------------------------
-- The single organization the access config's group bindings grant roles
-- in. Multi-org deployments add rows and bindings; nothing else changes.

INSERT INTO organizations (id, slug, display_name)
VALUES ('00000000-0000-0000-0000-000000000010', 'demo-org', 'Demo Organization')
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS den_link;
DROP TABLE IF EXISTS completions;
DROP TABLE IF EXISTS enrolments;
DROP TABLE IF EXISTS activities;
DROP TABLE IF EXISTS sections;
DROP TABLE IF EXISTS courses;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
