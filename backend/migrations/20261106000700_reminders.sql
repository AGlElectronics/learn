-- Learn — reminders by email (and later other channels).
--
-- reminders_sent dedupes the reminder worker: one row per reminder kind,
-- subject (enrolment cycle, session slot, certificate) and person, so each
-- reminder goes out once however often the worker runs. user_channels holds
-- per-person addresses on non-email channels (Telegram chat id), ready for
-- when those channels are implemented.

-- +goose Up
CREATE TABLE reminders_sent (
    kind       TEXT NOT NULL,
    subject_id UUID NOT NULL,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sent_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, subject_id, user_id)
);

CREATE INDEX reminders_sent_user_idx ON reminders_sent (user_id);

CREATE TABLE user_channels (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel    TEXT NOT NULL CHECK (channel <> ''),
    address    TEXT NOT NULL CHECK (address <> ''),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, channel)
);

-- +goose Down
DROP TABLE IF EXISTS user_channels;
DROP TABLE IF EXISTS reminders_sent;
