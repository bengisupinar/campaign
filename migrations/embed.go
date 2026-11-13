// Package migrations embeds the versioned SQL migrations that define the database schema.
//
// Files are named NNNNN_description.sql and contain "-- +goose Up" and "-- +goose Down"
// sections. Create one with `make migrate-new name=<description>`; the API applies pending
// migrations on startup.
package migrations

import "embed"

// FS contains every migration in this directory.
//
//go:embed *.sql
var FS embed.FS
