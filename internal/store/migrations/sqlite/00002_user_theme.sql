-- +goose Up
-- Color theme of the web UI per user: auto (follow the browser), light or dark.
ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'auto';

-- +goose Down
ALTER TABLE users DROP COLUMN theme;
