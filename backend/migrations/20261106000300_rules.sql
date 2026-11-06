-- Learn — activity prerequisites and due dates.
--
-- An activity can require other activities of the same course to be
-- complete first (no cycles; both checked by the service on save). A
-- course can set due_days: each learner enrolment then gets a due date
-- that many days after enrolling, which staff can override per learner.

-- +goose Up
CREATE TABLE activity_prerequisites (
    activity_id          UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    requires_activity_id UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    PRIMARY KEY (activity_id, requires_activity_id),
    CHECK (activity_id <> requires_activity_id)
);

CREATE INDEX activity_prerequisites_requires_idx ON activity_prerequisites (requires_activity_id);

ALTER TABLE courses ADD COLUMN due_days INTEGER CHECK (due_days IS NULL OR due_days > 0);
ALTER TABLE enrolments ADD COLUMN due_at TIMESTAMPTZ;

-- Every way of creating a learner enrolment (staff, self-enrol, group sync)
-- gets the course's due date unless one was given explicitly.
-- +goose StatementBegin
CREATE FUNCTION enrolments_set_due_at() RETURNS trigger AS $$
BEGIN
    IF NEW.due_at IS NULL AND NEW.role = 'learner' THEN
        SELECT now() + make_interval(days => c.due_days)
          INTO NEW.due_at
          FROM courses c
         WHERE c.id = NEW.course_id AND c.due_days IS NOT NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER enrolments_set_due_at
    BEFORE INSERT ON enrolments
    FOR EACH ROW EXECUTE FUNCTION enrolments_set_due_at();

-- +goose Down
DROP TRIGGER IF EXISTS enrolments_set_due_at ON enrolments;
DROP FUNCTION IF EXISTS enrolments_set_due_at();
ALTER TABLE enrolments DROP COLUMN IF EXISTS due_at;
ALTER TABLE courses DROP COLUMN IF EXISTS due_days;
DROP TABLE IF EXISTS activity_prerequisites;
