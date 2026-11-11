-- Learn — audit log.
--
-- One row per security- or record-relevant action: enrolments, course
-- roles, publishing, archiving, quiz grading and resets, certificate
-- revokes, group links, session sign-up status, Den pairing, settings and
-- erasure. Rows are written by the backend only and never edited; the
-- retention job deletes rows older than the configured audit retention.
--
-- actor_name is a copy of the actor's display name at the time, so the log
-- stays readable after a user is erased (erasure replaces it).

-- +goose Up
CREATE TABLE audit_log (
    id              BIGSERIAL PRIMARY KEY,
    at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_name      TEXT NOT NULL DEFAULT '',
    action          TEXT NOT NULL CHECK (action <> ''),
    target_type     TEXT NOT NULL DEFAULT '',
    target_id       TEXT NOT NULL DEFAULT '',
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    course_id       UUID,
    details         JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX audit_log_at_idx     ON audit_log (at DESC, id DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action, at DESC);
CREATE INDEX audit_log_course_idx ON audit_log (course_id, at DESC) WHERE course_id IS NOT NULL;
CREATE INDEX audit_log_actor_idx  ON audit_log (actor_user_id) WHERE actor_user_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS audit_log;
