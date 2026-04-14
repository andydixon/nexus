// Package migrations provides embedded SQL migration files for the storage layer.
package migrations

import "embed"

// MigrationFS contains all embedded SQL migration files.
// Each migration is numbered sequentially (e.g. 001_initial.sql).
//
//go:embed *.sql
var MigrationFS embed.FS
