// Copyright (c) 2026 Michael D Henderson.

package storage_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/storage"
)

// checkbookOnDisk gives a file-backed checkbook, closed, with one row in it so a
// read-only open has something to read back.
func checkbookOnDisk(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "checkbook.db")
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	conn, err := store.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	err = sqlitex.ExecuteTransient(conn,
		`INSERT INTO accounts (name, type, currency, opening_balance, created_at, updated_at)
		 VALUES ('Checking', 'checking', 'USD', 0, '2026-09-02T00:00:00.000000Z', '2026-09-02T00:00:00.000000Z');`, nil)
	store.Put(conn)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

// TestOpenReadOnlyReadsWithoutWriting is the whole point: a backup opened to be
// looked at must come back unchanged, down to its journal mode. Open would have
// migrated it and converted it to WAL, and a backup that has been rewritten is
// not the backup that was taken.
func TestOpenReadOnlyReadsWithoutWriting(t *testing.T) {
	// A backup, not the live checkbook: VACUUM INTO is what makes one, and what
	// it writes is a single self-contained file in "delete" journal mode. That
	// is what this has to leave alone. (A live checkbook is in WAL, and SQLite
	// builds the -wal and -shm sidecars it needs to read one; a read-only
	// connection cannot then clean them up. Backups are not in WAL, which is
	// why verifying one is read-only too.)
	path := vacuumCopy(t, checkbookOnDisk(t))

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := journalModeOf(t, path)

	store, err := storage.OpenReadOnly(t.Context(), path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if !store.ReadOnly() {
		t.Error("a read-only store does not report itself as one")
	}
	if store.IsMemory() {
		t.Error("a file-backed store reports itself as held in memory")
	}

	conn, err := store.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}

	var name string
	if err := sqlitex.ExecuteTransient(conn, `SELECT name FROM accounts;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { name = stmt.ColumnText(0); return nil },
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if name != "Checking" {
		t.Errorf("read back %q, want Checking", name)
	}

	// A write that arrives anyway is refused by SQLite itself, not merely by the
	// interface withholding the button.
	if err := sqlitex.ExecuteTransient(conn, `DELETE FROM accounts;`, nil); err == nil {
		t.Error("a read-only connection accepted a write")
	}

	store.Put(conn)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if before.Size() != after.Size() {
		t.Errorf("the file changed size, %d then %d", before.Size(), after.Size())
	}
	if got := journalModeOf(t, path); got != mode {
		t.Errorf("journal mode went from %q to %q; opening a backup rewrote it", mode, got)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			t.Errorf("opening read-only left a %s file beside the backup", suffix)
		}
	}
}

// TestOpenReadOnlyRefusesAnOlderSchema. Migrating it is a write, and a backup
// brought up to date is no longer the backup that was taken, so it is refused
// with something the reader can act on rather than quietly rewritten.
func TestOpenReadOnlyRefusesAnOlderSchema(t *testing.T) {
	path := checkbookOnDisk(t)
	setFileUserVersion(t, path, storage.MigrationCount()-1)

	_, err := storage.OpenReadOnly(t.Context(), path)
	if !errors.Is(err, storage.ErrDatabaseTooOld) {
		t.Fatalf("OpenReadOnly on an older schema = %v, want ErrDatabaseTooOld", err)
	}
}

func TestOpenReadOnlyRefusesANewerSchema(t *testing.T) {
	path := checkbookOnDisk(t)
	setFileUserVersion(t, path, storage.MigrationCount()+1)

	_, err := storage.OpenReadOnly(t.Context(), path)
	if !errors.Is(err, storage.ErrDatabaseTooNew) {
		t.Fatalf("OpenReadOnly on a newer schema = %v, want ErrDatabaseTooNew", err)
	}
}

// TestOpenReadOnlyRefusesAForeignDatabase: sqlitemigration makes this check for
// a read-write open and read-only never reaches it, so the guard has to be made
// again or the program would read somebody else's file as a checkbook.
func TestOpenReadOnlyRefusesAForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-ours.db")

	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		t.Fatalf("OpenConn: %v", err)
	}
	for _, q := range []string{
		`PRAGMA application_id = 305419896;`,
		`CREATE TABLE someone_elses_data (id INTEGER PRIMARY KEY);`,
	} {
		if err := sqlitex.ExecuteTransient(conn, q, nil); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := storage.OpenReadOnly(t.Context(), path); !errors.Is(err, storage.ErrNotCheckbook) {
		t.Fatalf("OpenReadOnly on a foreign database = %v, want ErrNotCheckbook", err)
	}
}

// TestOpenReadOnlyNeverCreates: OpenCreate is deliberately absent from the
// flags, so a mistyped path is reported rather than turned into an empty file.
func TestOpenReadOnlyNeverCreates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")

	if _, err := storage.OpenReadOnly(t.Context(), path); !errors.Is(err, storage.ErrMissingFile) {
		t.Fatalf("OpenReadOnly on a missing file = %v, want ErrMissingFile", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("OpenReadOnly created the file it was asked to read")
	}
	if _, err := storage.OpenReadOnly(t.Context(), ""); !errors.Is(err, storage.ErrMissingPath) {
		t.Error("OpenReadOnly with no path did not report ErrMissingPath")
	}
}

// vacuumCopy makes the kind of file backup.Create makes: a compacted,
// self-contained copy in the default journal mode.
func vacuumCopy(t *testing.T, src string) string {
	t.Helper()

	dest := filepath.Join(t.TempDir(), "checkbook-20260902-120000.db")

	conn, err := sqlite.OpenConn(src, sqlite.OpenReadOnly)
	if err != nil {
		t.Fatalf("OpenConn: %v", err)
	}
	defer conn.Close()

	if err := sqlitex.ExecuteTransient(conn, `VACUUM INTO ?;`, &sqlitex.ExecOptions{
		Args: []any{dest},
	}); err != nil {
		t.Fatalf("VACUUM INTO: %v", err)
	}
	return dest
}

// journalModeOf reads the journal mode a file itself carries.
func journalModeOf(t *testing.T, path string) string {
	t.Helper()

	conn, err := sqlite.OpenConn(path, sqlite.OpenReadOnly)
	if err != nil {
		t.Fatalf("OpenConn: %v", err)
	}
	defer conn.Close()

	var mode string
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error { mode = stmt.ColumnText(0); return nil },
	}); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	return mode
}

// setFileUserVersion moves a closed database's schema version, which is how a
// file from another release is simulated without keeping one in the tree.
func setFileUserVersion(t *testing.T, path string, version int) {
	t.Helper()

	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite)
	if err != nil {
		t.Fatalf("OpenConn: %v", err)
	}
	defer conn.Close()

	if err := sqlitex.ExecuteTransient(conn, fmt.Sprintf(`PRAGMA user_version = %d;`, version), nil); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
}

// TestCloseIsIdempotent. The program can reach a second Close honestly: the
// command defers one and the browser's Close checkbook does one of its own. The
// two pools behind a Store disagree about what a second Close means, and one of
// them panics, so the Store settles it.
func TestCloseIsIdempotent(t *testing.T) {
	for _, tt := range []struct {
		name string
		open func(t *testing.T) *storage.Store
	}{
		{"a migrated store", func(t *testing.T) *storage.Store {
			s, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "checkbook.db"))
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			return s
		}},
		{"a read-only store", func(t *testing.T) *storage.Store {
			s, err := storage.OpenReadOnly(t.Context(), vacuumCopy(t, checkbookOnDisk(t)))
			if err != nil {
				t.Fatalf("OpenReadOnly: %v", err)
			}
			return s
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.open(t)
			if err := store.Close(); err != nil {
				t.Fatalf("first Close: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Errorf("second Close: %v", err)
			}
		})
	}
}
