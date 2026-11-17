-- +goose Up
-- Archived switches come from an import (lanleecher import): they keep the
-- recorded ports and traffic but have no credentials and are never polled.
ALTER TABLE switches ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE switches DROP COLUMN archived;
