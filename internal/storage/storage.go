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
	"os"
	"path/filepath"
	"strings"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/cerrs"
)

const (
	// ErrMissingPath is returned when Open is called without a database path.
	ErrMissingPath = cerrs.Error("missing database path")

	// ErrMissingDirectory is returned when the directory that would hold the
	// database does not exist. Open never creates directories.
	ErrMissingDirectory = cerrs.Error("database directory does not exist")

	// ErrForeignKeysDisabled is returned when a connection reaches a caller
	// without foreign key enforcement.
	ErrForeignKeysDisabled = cerrs.Error("foreign key enforcement is off")

	// ErrJournalModeNotWAL is returned when a connection is not in WAL mode.
	ErrJournalModeNotWAL = cerrs.Error("journal mode is not wal")
)

// PoolSize is the number of connections the store keeps open.
//
// The local web UI serves one household from possibly several browser tabs, so
// requests genuinely overlap. This is set explicitly rather than left to the
// library default so that the pragma guarantees below can be tested across every
// connection a caller can be handed.
const PoolSize = 10

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
// Open creates the database file but never creates directories. If the parent
// directory does not exist, Open fails with ErrMissingDirectory rather than
// making it: a typo in a path, or a test with a stray relative path, must not
// scatter directories across the filesystem.
//
// Open blocks until the migrations finish so that a failure surfaces here rather
// than at the first query. It fails if the file exists but was written by another
// application, because its application_id will not match AppID.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, ErrMissingPath
	}

	// Checked before opening: SQLite would fail on a missing directory anyway,
	// but with an opaque "unable to open database file" that says nothing about
	// which part of the path was wrong.
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s: %w", dir, ErrMissingDirectory)
	}

	pool := sqlitemigration.NewPool(path, schema, sqlitemigration.Options{
		PoolSize:    PoolSize,
		PrepareConn: prepareConn,
		// Flags is deliberately left zero. sqlitex then applies its defaults,
		// which include sqlite.OpenWAL. Setting Flags here without repeating
		// OpenWAL would silently drop WAL; prepareConn verifies it regardless.
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

// prepareConn configures a connection and refuses to hand back one that is
// misconfigured.
//
// sqlitex calls this lazily, once per connection, on that connection's first
// Take, and returns the error rather than the connection if it fails. So every
// connection a caller can borrow has been through here.
//
// Both settings are verified rather than merely requested. A pragma that does
// not take effect is silent -- foreign keys would simply stop being enforced --
// and that is exactly the class of failure the register cannot afford.
func prepareConn(conn *sqlite.Conn) error {
	// SQLite defaults foreign key enforcement to OFF, per connection. The
	// REFERENCES clauses in the schema are inert without this, which would let
	// splits outlive the transaction they belong to.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA foreign_keys = ON;`, nil); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	// WAL lets a tab keep reading the register while another writes. It is a
	// persistent property of the file, so this is a no-op after the first
	// connection converts it.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode = WAL;`, nil); err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}

	enforcing, err := pragmaInt(conn, `PRAGMA foreign_keys;`)
	if err != nil {
		return err
	}
	if enforcing != 1 {
		return ErrForeignKeysDisabled
	}

	mode, err := pragmaText(conn, `PRAGMA journal_mode;`)
	if err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("%w: %s", ErrJournalModeNotWAL, mode)
	}

	return nil
}

// pragmaInt reads a pragma that returns a single integer.
func pragmaInt(conn *sqlite.Conn, query string) (int64, error) {
	var value int64
	err := sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			value = stmt.ColumnInt64(0)
			return nil
		},
	})
	if err != nil {
		return 0, fmt.Errorf("%s: %w", query, err)
	}
	return value, nil
}

// pragmaText reads a pragma that returns a single string.
func pragmaText(conn *sqlite.Conn, query string) (string, error) {
	var value string
	err := sqlitex.ExecuteTransient(conn, query, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			value = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		return "", fmt.Errorf("%s: %w", query, err)
	}
	return value, nil
}
