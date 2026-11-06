-- Learn — optional course and session feedback.
--
-- Off by default per course. When on, a learner leaves one 1-5 rating (and
-- an optional comment) after finishing the course, and one after attending
-- each session (activity_id set). dedupe_hash is sha256 of
-- course|activity|user|constant, so "one per person per target" holds even
-- for anonymous feedback, which never stores user_id.

-- +goose Up
ALTER TABLE courses
    ADD COLUMN feedback_enabled   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN feedback_anonymous BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE feedback (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id   UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    activity_id UUID REFERENCES activities(id) ON DELETE CASCADE,
    -- NULL when the course collects feedback anonymously.
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    dedupe_hash TEXT NOT NULL,
    rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    comment     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dedupe_hash)
);

CREATE INDEX feedback_course_idx ON feedback (course_id, activity_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS feedback;
ALTER TABLE courses
    DROP COLUMN IF EXISTS feedback_anonymous,
    DROP COLUMN IF EXISTS feedback_enabled;
