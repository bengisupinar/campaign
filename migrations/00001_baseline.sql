-- Baseline: starts goose's version tracking and proves the migration pipeline end to end.
-- Intentionally a no-op; the first table arrives with the first business capability.

-- +goose Up
SELECT 1;

-- +goose Down
SELECT 1;
