-- Learn — in-person sessions with QR check-in.
--
-- A session is an activity of kind 'session' with one or more time slots.
-- Learners book one slot per session (capacity + waitlist); staff show a
-- rotating QR at the venue, derived from the slot's checkin_secret, and a
-- valid scan marks the learner attended, which completes the activity.

-- +goose Up
ALTER TABLE activities DROP CONSTRAINT activities_kind_check;
ALTER TABLE activities ADD CONSTRAINT activities_kind_check CHECK (kind IN ('page', 'quiz', 'session'));

CREATE TABLE session_slots (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    activity_id    UUID NOT NULL REFERENCES activities(id) ON DELETE CASCADE,
    starts_at      TIMESTAMPTZ NOT NULL,
    ends_at        TIMESTAMPTZ NOT NULL,
    location       TEXT NOT NULL DEFAULT '',
    capacity       INTEGER NOT NULL CHECK (capacity > 0),
    -- Key for the rotating check-in code; never leaves the backend.
    checkin_secret BYTEA NOT NULL DEFAULT gen_random_bytes(32),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    UNIQUE (id, activity_id)
);

CREATE INDEX session_slots_activity_idx ON session_slots (activity_id, starts_at);

-- activity_id is denormalized from the slot (kept consistent by the
-- composite foreign key) so "one signup per session per person" is a plain
-- unique constraint. Waitlist order is created_at.
CREATE TABLE session_signups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slot_id     UUID NOT NULL,
    activity_id UUID NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status      TEXT NOT NULL DEFAULT 'booked' CHECK (status IN ('booked', 'waitlist', 'attended', 'no_show')),
    -- TRUE when the person checked in without a booking.
    walk_in     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (slot_id, activity_id) REFERENCES session_slots (id, activity_id) ON DELETE CASCADE ON UPDATE CASCADE,
    UNIQUE (activity_id, user_id)
);

CREATE INDEX session_signups_slot_idx ON session_signups (slot_id, status, created_at);
CREATE INDEX session_signups_user_idx ON session_signups (user_id);

-- +goose Down
DROP TABLE IF EXISTS session_signups;
DROP TABLE IF EXISTS session_slots;
DELETE FROM activities WHERE kind = 'session';
ALTER TABLE activities DROP CONSTRAINT activities_kind_check;
ALTER TABLE activities ADD CONSTRAINT activities_kind_check CHECK (kind IN ('page', 'quiz'));
