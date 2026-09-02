// Copyright (c) 2026 Michael D Henderson.

package storage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/storage"
)

// markAsBackup stamps a database's header the way backup.Create does, without
// reaching for that package: this test is about what storage does with such a
// file, not about how one is produced.
func markAsBackup(t *testing.T, path string) {
	t.Helper()

	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = conn.Close() }()

	if err := sqlitex.ExecuteTransient(conn, `PRAGMA application_id = 1296911742;`, nil); err != nil {
		t.Fatalf("stamp %s: %v", path, err)
	}
	// A real backup comes out of VACUUM INTO, which produces a rollback-journal
	// database, and backup.Create is careful not to convert it. checkbookOnDisk
	// left this one in WAL mode, so put it back: a test whose fixture is in the
	// wrong journal mode would find a -wal beside the file and blame the code.
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode = delete;`, nil); err != nil {
		t.Fatalf("set the journal mode of %s: %v", path, err)
	}
}

// TestOpenRefusesABackup is SPECIFICATION.md BK-6, and the bug it was written
// for: a backup opened for writing is migrated, converted to WAL, and served as
// a register, at which point it has stopped being the copy that was taken.
func TestOpenRefusesABackup(t *testing.T) {
	path := checkbookOnDisk(t)
	markAsBackup(t, path)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}

	store, err := storage.Open(t.Context(), path)
	if err == nil {
		_ = store.Close()
		t.Fatal("Open on a backup succeeded")
	}
	if !errors.Is(err, storage.ErrIsBackup) {
		t.Errorf("Open on a backup: %v, want ErrIsBackup", err)
	}

	// Refusing is only half of it. The file has to be exactly as it was found,
	// which is what a refusal that came too late would not manage.
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if after.Size() != before.Size() {
		t.Errorf("the backup changed size: %d, was %d", after.Size(), before.Size())
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the backup was written to: modified %s, was %s", after.ModTime(), before.ModTime())
	}
	// A -wal beside it is the fingerprint of the bug: VACUUM INTO produces a
	// rollback-journal database, and only opening one read-write converts it.
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("%s was created, so the backup was opened for writing", sidecar)
		}
	}
}

// TestOpenReadOnlyAcceptsABackup is the other half of the same rule. Refusing to
// write to a backup is only useful if reading one still works: that is how a
// household checks a backup is the one they want before restoring it.
func TestOpenReadOnlyAcceptsABackup(t *testing.T) {
	path := checkbookOnDisk(t)
	markAsBackup(t, path)

	store, err := storage.OpenReadOnly(t.Context(), path)
	if err != nil {
		t.Fatalf("OpenReadOnly on a backup: %v", err)
	}
	defer func() { _ = store.Close() }()

	if !store.IsBackup() {
		t.Error("IsBackup() = false on a file stamped as a backup")
	}
	if !store.ReadOnly() {
		t.Error("ReadOnly() = false on a read-only store")
	}
}

// TestOpenOrReadOnlyOpensABackupForReading. Whether a file is a backup is
// written in its header, so the caller that just wants it open does not have to
// be told: it is opened the one way it can be, and nothing is written to it.
func TestOpenOrReadOnlyOpensABackupForReading(t *testing.T) {
	path := checkbookOnDisk(t)
	markAsBackup(t, path)

	store, err := storage.OpenOrReadOnly(t.Context(), path)
	if err != nil {
		t.Fatalf("OpenOrReadOnly on a backup: %v", err)
	}
	defer func() { _ = store.Close() }()

	if !store.IsBackup() || !store.ReadOnly() {
		t.Errorf("IsBackup() = %v, ReadOnly() = %v; want both true", store.IsBackup(), store.ReadOnly())
	}
	// BK-6 as the filesystem sees it: opening for writing would have converted
	// the file to WAL and left a -wal beside it.
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("%s exists, so the backup was opened for writing", sidecar)
		}
	}
}

// TestOpenOrReadOnlyOpensACheckbookForWriting: the fallback is for backups and
// nothing else. An ordinary checkbook still opens the ordinary way, or the box
// on the open form would have nothing left to mean.
func TestOpenOrReadOnlyOpensACheckbookForWriting(t *testing.T) {
	store, err := storage.OpenOrReadOnly(t.Context(), checkbookOnDisk(t))
	if err != nil {
		t.Fatalf("OpenOrReadOnly: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.ReadOnly() || store.IsBackup() {
		t.Errorf("ReadOnly() = %v, IsBackup() = %v; want both false", store.ReadOnly(), store.IsBackup())
	}
}

// TestOpenReadOnlyOnAnOrdinaryCheckbookIsNotABackup: IsBackup narrows ReadOnly
// rather than restating it, and the two must not be allowed to blur.
func TestOpenReadOnlyOnAnOrdinaryCheckbookIsNotABackup(t *testing.T) {
	store, err := storage.OpenReadOnly(t.Context(), checkbookOnDisk(t))
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.IsBackup() {
		t.Error("IsBackup() = true on an ordinary checkbook opened read-only")
	}
}

// TestApplicationIDReportsWhatAFileIs covers the three answers callers act on,
// including the one that matters most: a path that is not there must be
// reported and never created.
func TestApplicationIDReportsWhatAFileIs(t *testing.T) {
	path := checkbookOnDisk(t)

	if id, err := storage.ApplicationID(path); err != nil || id != storage.AppID {
		t.Errorf("ApplicationID on a checkbook = %d, %v; want %d", id, err, storage.AppID)
	}

	markAsBackup(t, path)
	if id, err := storage.ApplicationID(path); err != nil || id != storage.BackupAppID {
		t.Errorf("ApplicationID on a backup = %d, %v; want %d", id, err, storage.BackupAppID)
	}

	absent := t.TempDir() + "/nowhere.db"
	if _, err := storage.ApplicationID(absent); !errors.Is(err, storage.ErrMissingFile) {
		t.Errorf("ApplicationID on a missing file: %v, want ErrMissingFile", err)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Error("ApplicationID created the file it was asked about")
	}
}

// TestOpenStillCreatesANewCheckbook: the guard reads the header of a file that
// is there, and must not turn a path that is not into a failure. Starting a new
// checkbook is a path that does not exist yet.
func TestOpenStillCreatesANewCheckbook(t *testing.T) {
	path := t.TempDir() + "/fresh.db"

	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open on a path that does not exist: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.IsBackup() {
		t.Error("a new checkbook reports itself as a backup")
	}
}

// TestOpenRefusesAFileThatIsNotADatabaseAtOnce, and "at once" is the assertion.
//
// sqlitemigration.Pool.Take retries a pool it could not open every five seconds
// for as long as its context lasts, and sqlitex.NewPool failing is one of the
// cases it retries rather than reports. Left to it, Open on a checkbook that has
// been truncated or overwritten does not fail -- it hangs, until the program is
// stopped, which for the browser means a listener that accepts and never
// answers. That is the worst possible response to the one emergency this program
// has, so the file is asked what it is before sqlitemigration ever sees it.
func TestOpenRefusesAFileThatIsNotADatabaseAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkbook.db")
	if err := os.WriteFile(path, []byte("this is not a database"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// A deadline far shorter than the five seconds a retry would take, so a
	// regression fails the test rather than slowing it down.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if _, err := storage.Open(ctx, path); !errors.Is(err, storage.ErrNotCheckbook) {
		t.Errorf("Open on a file that is not a database: %v, want ErrNotCheckbook", err)
	}
	if ctx.Err() != nil {
		t.Error("Open waited for its context rather than reporting at once")
	}
}

// TestOpenStillCreatesACheckbookInAnEmptyFile. The guard above must not catch
// this: an empty file is one SQLite will initialize, and "touch checkbook.db and
// start the program" is a reasonable way to begin.
func TestOpenStillCreatesACheckbookInAnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkbook.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open on an empty file: %v", err)
	}
	_ = store.Close()
}
