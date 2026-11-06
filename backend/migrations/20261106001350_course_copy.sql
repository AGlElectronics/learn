-- Learn — copy a course for a new edition.
--
-- A copied course gets its own media rows that point at the same stored
-- objects, so object_key is no longer unique. Deleting a media row only
-- removes the stored object when no other row still references the key.

-- +goose Up
ALTER TABLE media DROP CONSTRAINT media_object_key_key;
CREATE INDEX media_object_key_idx ON media (object_key);

-- +goose Down
DROP INDEX IF EXISTS media_object_key_idx;
ALTER TABLE media ADD CONSTRAINT media_object_key_key UNIQUE (object_key);
