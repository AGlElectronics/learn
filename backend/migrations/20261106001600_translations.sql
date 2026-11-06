-- Learn — several languages.
--
-- A course is written in its default language (the base columns) and may
-- offer more languages. Translations of learner-facing text live in one
-- table keyed by (entity_type, entity_id, field, lang); a missing row falls
-- back to the default text. Quiz marking is unaffected: answers are keyed
-- by option id, never by text. users.language is the person's saved UI and
-- content language (null = follow the browser).

-- +goose Up
ALTER TABLE courses ADD COLUMN default_lang TEXT NOT NULL DEFAULT 'en' CHECK (default_lang ~ '^[a-z]{2,3}$');
ALTER TABLE courses ADD COLUMN languages TEXT[] NOT NULL DEFAULT '{en}';
ALTER TABLE courses ADD CONSTRAINT courses_default_lang_offered CHECK (default_lang = ANY (languages));

ALTER TABLE users ADD COLUMN language TEXT CHECK (language IS NULL OR language ~ '^[a-z]{2,3}$');

CREATE TABLE translations (
    entity_type TEXT NOT NULL CHECK (entity_type IN (
        'course', 'section', 'activity', 'question', 'option', 'media',
        'activity_media', 'path', 'qualification')),
    entity_id   UUID NOT NULL,
    field       TEXT NOT NULL,
    lang        TEXT NOT NULL CHECK (lang ~ '^[a-z]{2,3}$'),
    text        TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_type, entity_id, field, lang)
);

CREATE INDEX translations_entity_lang_idx ON translations (entity_id, lang);

-- Translations have no foreign key (one table serves many entities), so
-- deleting a translated row removes its translations through a trigger.
-- +goose StatementBegin
CREATE FUNCTION delete_translations() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM translations WHERE entity_type = TG_ARGV[0] AND entity_id = OLD.id;
    RETURN OLD;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER courses_delete_translations AFTER DELETE ON courses
    FOR EACH ROW EXECUTE FUNCTION delete_translations('course');
CREATE TRIGGER sections_delete_translations AFTER DELETE ON sections
    FOR EACH ROW EXECUTE FUNCTION delete_translations('section');
CREATE TRIGGER activities_delete_translations AFTER DELETE ON activities
    FOR EACH ROW EXECUTE FUNCTION delete_translations('activity');
CREATE TRIGGER questions_delete_translations AFTER DELETE ON questions
    FOR EACH ROW EXECUTE FUNCTION delete_translations('question');
CREATE TRIGGER question_options_delete_translations AFTER DELETE ON question_options
    FOR EACH ROW EXECUTE FUNCTION delete_translations('option');
CREATE TRIGGER media_delete_translations AFTER DELETE ON media
    FOR EACH ROW EXECUTE FUNCTION delete_translations('media');
CREATE TRIGGER activity_media_delete_translations AFTER DELETE ON activity_media
    FOR EACH ROW EXECUTE FUNCTION delete_translations('activity_media');
CREATE TRIGGER paths_delete_translations AFTER DELETE ON paths
    FOR EACH ROW EXECUTE FUNCTION delete_translations('path');
CREATE TRIGGER qualifications_delete_translations AFTER DELETE ON qualifications
    FOR EACH ROW EXECUTE FUNCTION delete_translations('qualification');

-- +goose Down
DROP TRIGGER IF EXISTS qualifications_delete_translations ON qualifications;
DROP TRIGGER IF EXISTS paths_delete_translations ON paths;
DROP TRIGGER IF EXISTS activity_media_delete_translations ON activity_media;
DROP TRIGGER IF EXISTS media_delete_translations ON media;
DROP TRIGGER IF EXISTS question_options_delete_translations ON question_options;
DROP TRIGGER IF EXISTS questions_delete_translations ON questions;
DROP TRIGGER IF EXISTS activities_delete_translations ON activities;
DROP TRIGGER IF EXISTS sections_delete_translations ON sections;
DROP TRIGGER IF EXISTS courses_delete_translations ON courses;
DROP FUNCTION IF EXISTS delete_translations();
DROP TABLE IF EXISTS translations;
ALTER TABLE users DROP COLUMN IF EXISTS language;
ALTER TABLE courses DROP CONSTRAINT IF EXISTS courses_default_lang_offered;
ALTER TABLE courses DROP COLUMN IF EXISTS languages;
ALTER TABLE courses DROP COLUMN IF EXISTS default_lang;
