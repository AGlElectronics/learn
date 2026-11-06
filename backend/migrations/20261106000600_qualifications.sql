-- Learn — qualifications, certificates, expiry and renewal.
--
-- A qualification ("First aid") is defined per organization; a course can
-- award one. Finishing every activity of the course in the current cycle
-- issues a certificate (once per cycle). A certificate may expire after the
-- qualification's valid_months; renewing a course bumps the enrolment's
-- cycle, which resets progress without deleting history: completions and
-- quiz attempts carry the cycle they belong to. Nothing here is deleted
-- automatically; staff revoke certificates instead.

-- +goose Up
CREATE TABLE qualifications (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    -- Stable key other modules (HR role requirements) refer to.
    key             TEXT NOT NULL CHECK (key ~ '^[a-z0-9][a-z0-9_.-]*$'),
    name            TEXT NOT NULL CHECK (name <> ''),
    description     TEXT NOT NULL DEFAULT '',
    -- NULL = never expires.
    valid_months    INTEGER CHECK (valid_months IS NULL OR valid_months > 0),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key)
);

ALTER TABLE courses
    ADD COLUMN awards_qualification_id UUID REFERENCES qualifications(id) ON DELETE SET NULL;

ALTER TABLE enrolments ADD COLUMN cycle INTEGER NOT NULL DEFAULT 1 CHECK (cycle > 0);

ALTER TABLE completions ADD COLUMN cycle INTEGER NOT NULL DEFAULT 1 CHECK (cycle > 0);
ALTER TABLE completions DROP CONSTRAINT completions_activity_id_user_id_key;
ALTER TABLE completions ADD CONSTRAINT completions_activity_user_cycle_key UNIQUE (activity_id, user_id, cycle);

-- verify_code is the short public code printed on the certificate (QR to
-- /verify/{code}). course_id keeps the certificate when the course is
-- deleted; qualification_id can't be deleted while certificates exist.
CREATE TABLE certificates (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    qualification_id UUID NOT NULL REFERENCES qualifications(id) ON DELETE RESTRICT,
    course_id        UUID REFERENCES courses(id) ON DELETE SET NULL,
    cycle            INTEGER NOT NULL DEFAULT 1,
    issued_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- NULL = never expires.
    expires_at       TIMESTAMPTZ,
    verify_code      TEXT NOT NULL UNIQUE CHECK (verify_code ~ '^[A-Z2-7]{10,12}$'),
    revoked_at       TIMESTAMPTZ,
    revoked_reason   TEXT NOT NULL DEFAULT '',
    revoked_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (course_id, user_id, cycle)
);

CREATE INDEX certificates_user_idx ON certificates (user_id, qualification_id, issued_at DESC);
CREATE INDEX certificates_qualification_idx ON certificates (qualification_id);
CREATE INDEX certificates_expires_idx ON certificates (expires_at) WHERE revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS certificates;
DELETE FROM completions c USING completions newer
 WHERE c.activity_id = newer.activity_id AND c.user_id = newer.user_id AND c.cycle < newer.cycle;
ALTER TABLE completions DROP CONSTRAINT IF EXISTS completions_activity_user_cycle_key;
ALTER TABLE completions ADD CONSTRAINT completions_activity_id_user_id_key UNIQUE (activity_id, user_id);
ALTER TABLE completions DROP COLUMN IF EXISTS cycle;
ALTER TABLE enrolments DROP COLUMN IF EXISTS cycle;
ALTER TABLE courses DROP COLUMN IF EXISTS awards_qualification_id;
DROP TABLE IF EXISTS qualifications;
