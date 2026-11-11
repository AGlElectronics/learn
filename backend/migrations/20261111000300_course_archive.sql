-- Learn — archived courses.
--
-- Courses are never deleted. Archiving hides a course from learners and
-- every course list, and makes it read-only for its staff; unarchiving
-- brings it back exactly as it was. Enrolments, attempts and certificates
-- stay untouched either way.

-- +goose Up
ALTER TABLE courses
    ADD COLUMN archived_at TIMESTAMPTZ,
    ADD COLUMN archived_by UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX courses_archived_idx ON courses (archived_at) WHERE archived_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS courses_archived_idx;
ALTER TABLE courses DROP COLUMN IF EXISTS archived_by, DROP COLUMN IF EXISTS archived_at;
