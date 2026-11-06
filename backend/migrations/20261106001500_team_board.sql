-- Learn — team progress board.
--
-- Staff turn the board on per course or qualification and pick which
-- Keycloak groups (teams) it shows. The board only ever reports counts per
-- group (members, complete, percent) from the user_groups cache, never a
-- list or ranking of people.

-- +goose Up
ALTER TABLE courses ADD COLUMN team_board_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE qualifications ADD COLUMN team_board_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE team_board_groups (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    course_id        UUID REFERENCES courses(id) ON DELETE CASCADE,
    qualification_id UUID REFERENCES qualifications(id) ON DELETE CASCADE,
    group_name       TEXT NOT NULL CHECK (group_name <> ''),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((course_id IS NULL) <> (qualification_id IS NULL))
);

CREATE UNIQUE INDEX team_board_groups_course_idx ON team_board_groups (course_id, group_name) WHERE course_id IS NOT NULL;
CREATE UNIQUE INDEX team_board_groups_qualification_idx ON team_board_groups (qualification_id, group_name) WHERE qualification_id IS NOT NULL;
CREATE INDEX team_board_groups_group_idx ON team_board_groups (group_name);

-- +goose Down
DROP TABLE IF EXISTS team_board_groups;
ALTER TABLE qualifications DROP COLUMN IF EXISTS team_board_enabled;
ALTER TABLE courses DROP COLUMN IF EXISTS team_board_enabled;
