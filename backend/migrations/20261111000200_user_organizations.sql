-- Learn — which organization a person belongs to.
--
-- The people picker only shows people of the caller's organization. A
-- person belongs to an organization when they signed in through a binding
-- or the access config's home organization, or when they are enrolled on a
-- course of it (also when the group sync enrolled them). A trigger keeps the
-- enrolment half current.

-- +goose Up
CREATE TABLE user_organizations (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id)
);

CREATE INDEX user_organizations_org_idx ON user_organizations (organization_id);

INSERT INTO user_organizations (user_id, organization_id)
SELECT DISTINCT e.user_id, c.organization_id
  FROM enrolments e JOIN courses c ON c.id = e.course_id
ON CONFLICT DO NOTHING;

-- A single-organization install (the default): everyone already known
-- belongs to it.
INSERT INTO user_organizations (user_id, organization_id)
SELECT u.id, o.id FROM users u CROSS JOIN organizations o
 WHERE (SELECT count(*) FROM organizations) = 1
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
CREATE FUNCTION learn_enrolment_user_org() RETURNS trigger AS $$
BEGIN
    INSERT INTO user_organizations (user_id, organization_id)
    SELECT NEW.user_id, c.organization_id FROM courses c WHERE c.id = NEW.course_id
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER enrolments_user_org
AFTER INSERT ON enrolments
FOR EACH ROW EXECUTE FUNCTION learn_enrolment_user_org();

-- +goose Down
DROP TRIGGER IF EXISTS enrolments_user_org ON enrolments;
DROP FUNCTION IF EXISTS learn_enrolment_user_org();
DROP TABLE IF EXISTS user_organizations;
