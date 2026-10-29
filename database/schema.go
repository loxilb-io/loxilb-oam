// Package database carries the PostgreSQL schema inside the binary: the frozen
// baseline and the numbered migrations that follow it. See internal/migrate
// for how they are applied.
package database

import "embed"

// BaselineVersion is the schema version produced by BaselineSQL.
const BaselineVersion = 1

// BaselineSQL is the complete schema of version 1. It is the same file the
// PostgreSQL container runs from /docker-entrypoint-initdb.d on an empty data
// directory, embedded so the server can also initialize an empty database
// itself.
//
// The file is frozen: every change after version 1 is a new file under
// migrations/postgres, never an edit here. A database created from an edited
// baseline would claim version 1 while differing from every other version-1
// database, and no migration could tell the two apart.
//
//go:embed init/00-init-complete.sql
var BaselineSQL string

// Migrations holds migrations/postgres/NNNN_name.sql, versions 2 and up.
//
//go:embed migrations/postgres
var Migrations embed.FS

// MigrationsDir is the directory inside Migrations that holds the .sql files.
const MigrationsDir = "migrations/postgres"
