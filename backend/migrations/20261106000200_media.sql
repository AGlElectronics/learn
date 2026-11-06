-- Learn — pictures, audio and video.
--
-- Files live in object storage (MinIO in dev); this table is the per-course
-- media library. The browser uploads straight to storage with a presigned
-- POST and the row stays 'pending' until the backend has confirmed the
-- object. Nothing is ever deleted automatically: pending and unused files
-- show up in the library under "Unused" and staff delete them by hand.
-- Object keys are random UUIDs, so a file name can never give an answer
-- away; `name` (the original file name) is shown to staff only.

-- +goose Up
CREATE TABLE media (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id    UUID NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('image', 'audio', 'video')),
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL CHECK (size_bytes > 0),
    object_key   TEXT NOT NULL UNIQUE,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready')),
    -- Images need alt text before they can be attached to anything.
    alt_text     TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX media_course_idx ON media (course_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- MEDIA IN QUESTIONS
-- ----------------------------------------------------------------------------
-- Media references use the default NO ACTION so deleting a whole course
-- still cascades; the service refuses to delete media that is in use.

ALTER TABLE questions DROP CONSTRAINT questions_kind_check;
ALTER TABLE questions ADD CONSTRAINT questions_kind_check CHECK (kind IN ('single', 'multiple', 'hotspot'));

ALTER TABLE questions
    ADD COLUMN media_id          UUID REFERENCES media(id),
    ADD COLUMN feedback_media_id UUID REFERENCES media(id),
    -- Hotspot: how many taps the learner gets. NULL = one per correct region.
    ADD COLUMN hotspot_clicks    INTEGER CHECK (hotspot_clicks IS NULL OR hotspot_clicks > 0);

-- An option is a label, a picture, or both.
ALTER TABLE question_options
    ALTER COLUMN label SET DEFAULT '',
    ADD COLUMN media_id UUID REFERENCES media(id);

CREATE INDEX questions_media_idx          ON questions (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX questions_feedback_media_idx ON questions (feedback_media_id) WHERE feedback_media_id IS NOT NULL;
CREATE INDEX question_options_media_idx   ON question_options (media_id) WHERE media_id IS NOT NULL;

-- Hotspot regions, drawn on the question's image. Coordinates are
-- normalized to 0..1 of the image so they work at any display size:
--   rect   {"x","y","w","h"}   (top-left corner + size)
--   circle {"cx","cy","rx","ry"} (an ellipse in normalized space, which is
--          a true circle at the image's own aspect ratio)
CREATE TABLE question_regions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    shape       TEXT NOT NULL CHECK (shape IN ('rect', 'circle')),
    coords      JSONB NOT NULL,
    correct     BOOLEAN NOT NULL DEFAULT TRUE,
    position    INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX question_regions_question_idx ON question_regions (question_id, position);

-- The learner's taps on a hotspot question: [{"x":0.1,"y":0.2}, ...].
ALTER TABLE quiz_attempt_questions ADD COLUMN taps JSONB;

-- ----------------------------------------------------------------------------
-- MEDIA IN PAGES
-- ----------------------------------------------------------------------------
-- A page shows its attachments under the body: uploaded media, or an
-- external video embed (YouTube / Vimeo only, played privacy-friendly).

CREATE TABLE activity_media (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    activity_id  UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    media_id     UUID REFERENCES media(id),
    external_url TEXT,
    position     INTEGER NOT NULL DEFAULT 0,
    caption      TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((media_id IS NULL) <> (external_url IS NULL))
);

CREATE INDEX activity_media_activity_idx ON activity_media (activity_id, position);
CREATE INDEX activity_media_media_idx    ON activity_media (media_id) WHERE media_id IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS activity_media;
ALTER TABLE quiz_attempt_questions DROP COLUMN IF EXISTS taps;
DROP TABLE IF EXISTS question_regions;
DELETE FROM quiz_attempt_questions
 WHERE question_id IN (SELECT id FROM questions WHERE kind = 'hotspot');
DELETE FROM questions WHERE kind = 'hotspot';
ALTER TABLE question_options DROP COLUMN IF EXISTS media_id;
ALTER TABLE question_options ALTER COLUMN label DROP DEFAULT;
ALTER TABLE questions
    DROP COLUMN IF EXISTS hotspot_clicks,
    DROP COLUMN IF EXISTS feedback_media_id,
    DROP COLUMN IF EXISTS media_id;
ALTER TABLE questions DROP CONSTRAINT questions_kind_check;
ALTER TABLE questions ADD CONSTRAINT questions_kind_check CHECK (kind IN ('single', 'multiple'));
DROP TABLE IF EXISTS media;
