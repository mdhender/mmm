// Copyright (c) 2026 Michael D Henderson.

// Package storage opens and migrates the checkbook database.
//
// The canonical records live in a single local SQLite file (SPECIFICATION.md
// ST-1, ST-2). This package owns the schema and the connection pool; it does not
// know anything about accounts or transactions beyond their columns. Domain
// packages build on it.
package storage

import (
	"context"
	"fmt"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/cerrs"
)

const (
	// ErrMissingPath is returned when Open is called without a database path.
	ErrMissingPath = cerrs.Error("missing database path")
)

// Store is a migrated checkbook database.
//
// A Store is safe for concurrent use: callers borrow a connection with Conn and
// must return it with Put.
type Store struct {
	path string
	pool *sqlitemigration.Pool
}

// Open opens the database at path, creating it if it does not exist, and brings
// its schema up to date.
//
// Open blocks until the migrations finish so that a failure surfaces here rather
// than at the first query. It fails if the file exists but was written by another
// application, because its application_id will not match AppID.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, ErrMissingPath
	}

	pool := sqlitemigration.NewPool(path, schema, sqlitemigration.Options{
		PrepareConn: prepareConn,
	})

	// Borrowing a connection waits for migration to complete and reports any
	// error it hit. Without this, Open would succeed on a database it could
	// never actually use.
	conn, err := pool.Get(ctx)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	pool.Put(conn)

	return &Store{path: path, pool: pool}, nil
}

// Path reports the database file this Store was opened from.
//
// The UI is required to show it (SPECIFICATION.md BK-3): a household that cannot
// tell which file it is editing cannot back the right one up.
func (s *Store) Path() string { return s.path }

// Conn borrows a connection from the pool. The caller must return it with Put,
// conventionally via defer.
func (s *Store) Conn(ctx context.Context) (*sqlite.Conn, error) {
	return s.pool.Get(ctx)
}

// Put returns a connection borrowed from Conn.
func (s *Store) Put(conn *sqlite.Conn) { s.pool.Put(conn) }

// Close releases every connection in the pool.
func (s *Store) Close() error { return s.pool.Close() }

// prepareConn configures each connection as it enters the pool.
//
// Both pragmas are per-connection or need an explicit statement, so neither can
// be expressed in the migration SQL.
func prepareConn(conn *sqlite.Conn) error {
	// SQLite defaults foreign key enforcement to OFF, per connection. The
	// REFERENCES clauses in the schema are inert without this, which would let
	// splits outlive the transaction they belong to.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA foreign_keys = ON;`, nil); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	// WAL lets the register keep reading while an import writes. It is a
	// persistent property of the file; setting it per connection is harmless.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode = WAL;`, nil); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	return nil
}
