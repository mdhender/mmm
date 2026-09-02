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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	// ErrInvalidMemoryName is returned when a shared in-memory database is given
	// a name that cannot be embedded in a URI.
	ErrInvalidMemoryName = cerrs.Error("invalid in-memory database name")

	// ErrForeignKeysDisabled is returned when a connection reaches a caller
	// without foreign key enforcement.
	ErrForeignKeysDisabled = cerrs.Error("foreign key enforcement is off")

	// ErrJournalModeNotWAL is returned when a connection is not in WAL mode.
	ErrJournalModeNotWAL = cerrs.Error("journal mode is not wal")

	// ErrNotCheckbook is returned when the file is a SQLite database that this
	// program did not create.
	ErrNotCheckbook = cerrs.Error("file is not a checkbook database")

	// ErrDatabaseTooNew is returned when the database carries a schema from a
	// newer release than this one knows about.
	ErrDatabaseTooNew = cerrs.Error("database was written by a newer version of the program")

	// ErrSchemaVersion is returned when the schema is not at the version this
	// program expects and is not simply newer.
	ErrSchemaVersion = cerrs.Error("unexpected schema version")

	// ErrMissingFile is returned when the database to be opened read-only is not
	// there. Read-only never creates one: a mistyped path is reported, not built.
	ErrMissingFile = cerrs.Error("database file does not exist")

	// ErrDatabaseTooOld is returned by OpenReadOnly when the database carries a
	// schema from an older release. It is not an error for Open, which migrates
	// it; read-only is where it has nowhere to go, because bringing it up to
	// date is a write and a backup that has been rewritten is not a backup.
	ErrDatabaseTooOld = cerrs.Error("database was written by an older version of the program")
)

// PoolSize is the number of connections the store keeps open.
//
// The local web UI serves one household from possibly several browser tabs, so
// requests genuinely overlap. This is set explicitly rather than left to the
// library default so that the pragma guarantees below can be tested across every
// connection a caller can be handed.
const PoolSize = 10

// pool is the connection pool behind a Store.
//
// It is an interface with three methods because a Store is opened two ways: a
// sqlitemigration.Pool, which brings the schema up to date, and a plain
// sqlitex.Pool opened read-only, which must not. Both carry exactly these three,
// so nothing else in this package changes shape.
type pool interface {
	Take(ctx context.Context) (*sqlite.Conn, error)
	Put(conn *sqlite.Conn)
	Close() error
}

// Store is a migrated checkbook database.
//
// A Store is safe for concurrent use: callers borrow a connection with Conn and
// must return it with Put.
type Store struct {
	path     string
	inMemory bool
	readOnly bool
	pool     pool

	// closeOnce makes Close idempotent. The two pools disagree about a second
	// Close -- sqlitemigration returns "already closed", sqlitex panics on a
	// channel it has already shut -- and the program can genuinely reach it
	// twice, because the command defers a close and the browser's Close
	// checkbook does one of its own.
	closeOnce sync.Once
	closeErr  error
}

// Open opens the database at path, creating it if it does not exist, and brings
// its schema up to date.
//
// Open opens the file-backed database at path. Tests that do not need a file
// should use OpenMemory instead.
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

	return open(ctx, path, path, PoolSize, false)
}

// OpenMemory opens an in-memory database. It touches no files and vanishes when
// the Store is closed, which is what tests want.
//
// ZombieZen refuses a bare ":memory:" outright, because each connection in a pool
// would silently get its own separate database. The two usable shapes are
// selected by name:
//
//   - A non-empty name gives a SHARED database: every connection in the Store
//     sees the same data, so the full pool is available and the Store behaves
//     like the file-backed one. The name is process-wide, so two tests sharing a
//     name share a database — pass something unique. t.Name() suits, but a
//     subtest's name contains "/" and must be sanitized: names are limited to
//     letters, digits, "-", "_", and "." so that a name can never introduce a
//     URI query parameter and quietly turn the database into a file.
//
//   - An empty name gives a PRIVATE database, which cannot be shared between
//     connections at all. The Store is therefore limited to a single connection.
//     Use this for tests that never need concurrency; it cannot collide with
//     anything.
//
// In-memory databases do not support WAL and report a journal mode of "memory".
// That is expected here and is not treated as the misconfiguration it would be
// for a file (see prepareConn). Foreign keys are enforced exactly as they are on
// disk, so constraint behavior under test matches production.
func OpenMemory(ctx context.Context, name string) (*Store, error) {
	if name == "" {
		// Private: unique so that nothing can accidentally reach it, and no
		// cache=shared so each connection would get its own database — hence a
		// pool of exactly one.
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		uri := "file:mmm-private-" + token + "?mode=memory"
		return open(ctx, uri, ":memory:", 1, true)
	}

	if !validMemoryName(name) {
		return nil, fmt.Errorf("%q: %w", name, ErrInvalidMemoryName)
	}
	uri := "file:" + name + "?mode=memory&cache=shared"
	return open(ctx, uri, ":memory:"+name, PoolSize, true)
}

// OpenReadOnly opens the database at path without migrating it and without ever
// writing to it.
//
// This is how a backup is looked at. Open migrates on open, so merely opening an
// older copy to see what is in it would rewrite the artifact -- and a backup that
// has been rewritten is not the backup that was taken. A database from an older
// release is therefore refused here with ErrDatabaseTooOld rather than brought
// up to date; the way forward is to copy it and open the copy read-write.
//
// Note the absence of OpenCreate in the flags: a path that does not exist fails
// instead of quietly becoming an empty database.
func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, ErrMissingPath
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return nil, fmt.Errorf("%s: %w", path, ErrMissingFile)
	}

	// OpenURI is deliberately absent too: path is a filesystem path, and a "?"
	// in a folder name must stay part of the name rather than becoming a
	// parameter.
	p, err := sqlitex.NewPool(path, sqlitex.PoolOptions{
		Flags:       sqlite.OpenReadOnly,
		PoolSize:    PoolSize,
		PrepareConn: prepareConnFunc(false, true),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	store := &Store{path: path, readOnly: true, pool: p}

	// Borrowing a connection is what runs prepareConn and reports anything wrong
	// with the file, so a failure surfaces here rather than at the first query.
	conn, err := store.Conn(ctx)
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("%s: %w", path, classifyOpenError(err))
	}
	appID, aerr := pragmaInt(conn, `PRAGMA application_id;`)
	schemaVersion, verr := pragmaInt(conn, `PRAGMA user_version;`)
	store.Put(conn)

	for _, err := range []error{aerr, verr} {
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	// sqlitemigration does this for a read-write open. Read-only never reaches
	// it, so the guard that stops the program reading an unrelated SQLite file
	// as a checkbook has to be made here.
	if int32(appID) != AppID {
		p.Close()
		return nil, fmt.Errorf("%s: %w: application_id is %d, not %d", path, ErrNotCheckbook, appID, AppID)
	}
	if err := checkReadOnlySchemaVersion(schemaVersion); err != nil {
		p.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return store, nil
}

// checkReadOnlySchemaVersion compares a read-only database's user_version
// against the number of migrations this build carries.
//
// Older is refused rather than migrated, which is the whole point of opening
// read-only. Newer is refused for the reason it always is: this build has never
// seen that schema, and the first symptom would be a wrong answer.
func checkReadOnlySchemaVersion(schemaVersion int64) error {
	want := int64(MigrationCount())
	switch {
	case schemaVersion > want:
		return fmt.Errorf("%w: database is at schema %d, this program understands %d",
			ErrDatabaseTooNew, schemaVersion, want)
	case schemaVersion < want:
		return fmt.Errorf("%w: database is at schema %d, this program expects %d",
			ErrDatabaseTooOld, schemaVersion, want)
	}
	return nil
}

// open builds a Store over uri. displayPath is what Path reports, poolSize is the
// number of connections, and inMemory relaxes the WAL requirement.
func open(ctx context.Context, uri, displayPath string, poolSize int, inMemory bool) (*Store, error) {
	pool := sqlitemigration.NewPool(uri, schema, sqlitemigration.Options{
		PoolSize:    poolSize,
		PrepareConn: prepareConnFunc(inMemory, false),
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
		return nil, fmt.Errorf("%s: %w", displayPath, classifyOpenError(err))
	}

	// The schema version is checked here, not left to sqlitemigration.
	// sqlitemigration only ever migrates forwards: its loop runs while
	// user_version is *below* the number of migrations, so a database written by
	// a later release -- one carrying migrations this build has never heard of --
	// falls straight through it and is opened as if nothing were wrong. The
	// program would then run against a schema it does not understand, and the
	// first symptom could be a query returning the wrong answer rather than an
	// error. Refuse it here instead.
	schemaVersion, verr := pragmaInt(conn, `PRAGMA user_version;`)
	// Return the connection before closing the pool: Close blocks until every
	// borrowed connection is back.
	pool.Put(conn)
	if verr != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: %w", displayPath, verr)
	}
	if err := checkSchemaVersion(schemaVersion); err != nil {
		pool.Close()
		return nil, fmt.Errorf("%s: %w", displayPath, err)
	}

	return &Store{path: displayPath, inMemory: inMemory, pool: pool}, nil
}

// checkSchemaVersion compares the database's user_version against the number of
// migrations this build carries.
func checkSchemaVersion(schemaVersion int64) error {
	want := int64(MigrationCount())
	switch {
	case schemaVersion > want:
		return fmt.Errorf("%w: database is at schema %d, this program understands %d",
			ErrDatabaseTooNew, schemaVersion, want)
	case schemaVersion < want:
		// Unreachable by way of a successful migration, so reaching it means
		// something interrupted or rewrote the schema. It is not safe to guess.
		return fmt.Errorf("%w: database is at schema %d, this program expects %d",
			ErrSchemaVersion, schemaVersion, want)
	}
	return nil
}

// classifyOpenError turns sqlitemigration's failure into one this program's
// callers can match on, so the UI can say something more useful than the raw
// text.
//
// The application_id mismatch is recognized by its message because
// sqlitemigration reports it as a plain error with no sentinel to compare
// against. That is fragile, and deliberately not load-bearing: if the wording
// ever changes the error simply stays generic, and the caller falls back to
// "the database could not be opened". TestOpenRejectsForeignDatabase pins the
// mapping so the change is noticed here rather than by a user.
func classifyOpenError(err error) error {
	if strings.Contains(err.Error(), "application_id") {
		return fmt.Errorf("%w: %v", ErrNotCheckbook, err)
	}
	return err
}

// validMemoryName reports whether name is safe to embed in a database URI.
// Anything that could introduce a query parameter is rejected.
func validMemoryName(name string) bool {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// randomToken returns a short hex string used to keep private in-memory
// databases from colliding.
func randomToken() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate database name: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Path reports the database file this Store was opened from.
//
// The UI is required to show it (SPECIFICATION.md BK-3): a household that cannot
// tell which file it is editing cannot back the right one up.
func (s *Store) Path() string { return s.path }

// IsMemory reports whether the database is held in memory, and so is discarded
// when the program stops.
//
// The interface shows this rather than leaving it to the path: a register that
// keeps nothing looks exactly like one that does, and the difference is the
// whole difference. It is a property of the database, not of a flag, so it stays
// true however the Store was opened.
func (s *Store) IsMemory() bool { return s.inMemory }

// ReadOnly reports whether the database was opened without the ability to write
// to it, which is how a backup is looked at.
//
// The interface shows this the way it shows IsMemory, and for the same reason: a
// register that cannot be written to looks exactly like one that can until
// somebody tries. Every write action is withheld rather than offered and then
// refused.
func (s *Store) ReadOnly() bool { return s.readOnly }

// Conn borrows a connection from the pool. The caller must return it with Put,
// conventionally via defer.
func (s *Store) Conn(ctx context.Context) (*sqlite.Conn, error) {
	return s.pool.Take(ctx)
}

// Put returns a connection borrowed from Conn.
func (s *Store) Put(conn *sqlite.Conn) { s.pool.Put(conn) }

// Close releases every connection in the pool.
//
// It is safe to call more than once: the second and later calls report what the
// first one did, rather than reaching a pool that has already been shut.
func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.pool.Close() })
	return s.closeErr
}

// prepareConnFunc builds the pool's connection setup hook.
func prepareConnFunc(inMemory, readOnly bool) func(*sqlite.Conn) error {
	return func(conn *sqlite.Conn) error { return prepareConn(conn, inMemory, readOnly) }
}

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
func prepareConn(conn *sqlite.Conn, inMemory, readOnly bool) error {
	// SQLite defaults foreign key enforcement to OFF, per connection. The
	// REFERENCES clauses in the schema are inert without this, which would let
	// splits outlive the transaction they belong to.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA foreign_keys = ON;`, nil); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	// WAL lets a tab keep reading the register while another writes. It is a
	// persistent property of the file, so this is a no-op after the first
	// connection converts it. In-memory databases do not support it at all.
	//
	// A read-only connection cannot set it and must not: the journal mode is a
	// property of the file, and changing it would be writing to the backup this
	// connection exists to avoid writing to.
	if !inMemory && !readOnly {
		if err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode = WAL;`, nil); err != nil {
			return fmt.Errorf("enable WAL: %w", err)
		}
	}

	enforcing, err := pragmaInt(conn, `PRAGMA foreign_keys;`)
	if err != nil {
		return err
	}
	if enforcing != 1 {
		return ErrForeignKeysDisabled
	}

	// Foreign keys are checked for every store; WAL only where it is possible,
	// so that an in-memory test still exercises the same constraint behavior. A
	// read-only connection reports whatever the file itself says -- "delete" for
	// anything VACUUM INTO produced -- and that is not a misconfiguration.
	if !inMemory && !readOnly {
		mode, err := pragmaText(conn, `PRAGMA journal_mode;`)
		if err != nil {
			return err
		}
		if !strings.EqualFold(mode, "wal") {
			return fmt.Errorf("%w: %s", ErrJournalModeNotWAL, mode)
		}
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
