-- Learn — quizzes, question bank and attempts.
--
-- A quiz is an activity of kind 'quiz'. Questions live in a per-course bank
-- (categories → questions → options) so copying a course stays simple. A
-- quiz is an ordered list of slots: one fixed question, or "draw N at
-- random from a category". The questions and option order of an attempt
-- are frozen when it starts, so reloading never reshuffles.

-- +goose Up
ALTER TABLE activities DROP CONSTRAINT activities_kind_check;
ALTER TABLE activities ADD CONSTRAINT activities_kind_check CHECK (kind IN ('page', 'quiz'));

ALTER TABLE activities
    ADD COLUMN pass_percent    INTEGER NOT NULL DEFAULT 50 CHECK (pass_percent BETWEEN 0 AND 100),
    -- NULL = unlimited attempts.
    ADD COLUMN max_attempts    INTEGER CHECK (max_attempts IS NULL OR max_attempts > 0),
    ADD COLUMN shuffle_options BOOLEAN NOT NULL DEFAULT TRUE;

-- ----------------------------------------------------------------------------
-- QUESTION BANK
-- ----------------------------------------------------------------------------

CREATE TABLE question_categories (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id  UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX question_categories_course_idx ON question_categories (course_id, position);

CREATE TABLE questions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id UUID NOT NULL REFERENCES question_categories(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL DEFAULT 'single' CHECK (kind IN ('single', 'multiple')),
    prompt      TEXT NOT NULL,
    -- Shown with the result after submitting.
    feedback    TEXT NOT NULL DEFAULT '',
    points      INTEGER NOT NULL DEFAULT 1 CHECK (points > 0),
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX questions_category_idx ON questions (category_id, position);

CREATE TABLE question_options (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    label       TEXT NOT NULL,
    correct     BOOLEAN NOT NULL DEFAULT FALSE,
    position    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX question_options_question_idx ON question_options (question_id, position);

-- ----------------------------------------------------------------------------
-- QUIZ CONTENT
-- ----------------------------------------------------------------------------

CREATE TABLE quiz_slots (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL DEFAULT 0,
    question_id UUID REFERENCES questions(id) ON DELETE CASCADE,
    category_id UUID REFERENCES question_categories(id) ON DELETE CASCADE,
    draw_count  INTEGER,
    CHECK (
        (question_id IS NOT NULL AND category_id IS NULL AND draw_count IS NULL)
        OR (question_id IS NULL AND category_id IS NOT NULL AND draw_count > 0)
    )
);

CREATE INDEX quiz_slots_activity_idx ON quiz_slots (activity_id, position);

-- ----------------------------------------------------------------------------
-- ATTEMPTS
-- ----------------------------------------------------------------------------
-- An attempt is open while submitted_at IS NULL; a learner has at most one
-- open attempt per quiz. score/max_score/passed are set on submit. cycle is
-- reserved for renewal (a later phase); every attempt is cycle 1 for now.

CREATE TABLE quiz_attempts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    activity_id  UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cycle        INTEGER NOT NULL DEFAULT 1,
    started_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    submitted_at TIMESTAMPTZ,
    score        INTEGER,
    max_score    INTEGER,
    passed       BOOLEAN
);

CREATE INDEX quiz_attempts_activity_user_idx ON quiz_attempts (activity_id, user_id);
CREATE UNIQUE INDEX quiz_attempts_one_open_idx ON quiz_attempts (activity_id, user_id) WHERE submitted_at IS NULL;

-- question_id has no cascade: a question that appears in attempts can't be
-- deleted until the quiz is reset (the service answers 409 first).
CREATE TABLE quiz_attempt_questions (
    attempt_id        UUID NOT NULL REFERENCES quiz_attempts(id) ON DELETE CASCADE,
    position          INTEGER NOT NULL,
    question_id       UUID NOT NULL REFERENCES questions(id),
    option_order      UUID[] NOT NULL DEFAULT '{}',
    chosen_option_ids UUID[] NOT NULL DEFAULT '{}',
    correct           BOOLEAN,
    points_awarded    INTEGER,
    PRIMARY KEY (attempt_id, question_id)
);

CREATE INDEX quiz_attempt_questions_question_idx ON quiz_attempt_questions (question_id);

-- +goose Down
DROP TABLE IF EXISTS quiz_attempt_questions;
DROP TABLE IF EXISTS quiz_attempts;
DROP TABLE IF EXISTS quiz_slots;
DROP TABLE IF EXISTS question_options;
DROP TABLE IF EXISTS questions;
DROP TABLE IF EXISTS question_categories;
DELETE FROM activities WHERE kind <> 'page';
ALTER TABLE activities
    DROP COLUMN IF EXISTS shuffle_options,
    DROP COLUMN IF EXISTS max_attempts,
    DROP COLUMN IF EXISTS pass_percent;
ALTER TABLE activities DROP CONSTRAINT activities_kind_check;
ALTER TABLE activities ADD CONSTRAINT activities_kind_check CHECK (kind IN ('page'));
