// Copyright (c) 2026 Michael D Henderson.

package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// backedUp seeds a checkbook, takes a backup of it, and returns both paths.
func backedUp(t *testing.T) (src, copied string) {
	t.Helper()

	src = seeded(t)
	res, err := backup.Create(t.Context(), src, filepath.Dir(src))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return src, res.Path
}

// TestBackupCannotBeOpenedForWriting is SPECIFICATION.md BK-6 at the point it
// matters: what Create produces must be refused by the ordinary open path, not
// merely named in a way that suggests it should be.
func TestBackupCannotBeOpenedForWriting(t *testing.T) {
	_, copied := backedUp(t)

	store, err := storage.Open(t.Context(), copied)
	if err == nil {
		_ = store.Close()
		t.Fatal("the backup opened for writing")
	}
	if !errors.Is(err, storage.ErrIsBackup) {
		t.Errorf("opening the backup: %v, want ErrIsBackup", err)
	}
}

// TestBackupIsOneFile: a backup is meant to be copied to another disk, and a
// WAL-mode database is not one file until it has been checkpointed. Stamping it
// is a write, so this is the guard that the write did not convert it.
func TestBackupIsOneFile(t *testing.T) {
	_, copied := backedUp(t)

	for _, sidecar := range []string{copied + "-wal", copied + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("%s was left beside the backup", filepath.Base(sidecar))
		}
	}
}

// TestRestoreRoundTrip is BK-4 end to end: the records in a backup come back
// into use as an ordinary checkbook, which can be opened and written to.
func TestRestoreRoundTrip(t *testing.T) {
	_, copied := backedUp(t)
	dest := filepath.Join(filepath.Dir(copied), "restored.db")

	res, err := backup.Restore(t.Context(), copied, dest)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if res.Path != dest {
		t.Errorf("Restore wrote %s, want %s", res.Path, dest)
	}

	// Read-write, which is the whole difference: this is a checkbook now.
	store, err := storage.Open(t.Context(), dest)
	if err != nil {
		t.Fatalf("open the restored checkbook: %v", err)
	}
	defer func() { _ = store.Close() }()

	if store.IsBackup() {
		t.Error("the restored file is still stamped as a backup")
	}

	accounts, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Checking" {
		t.Fatalf("the restored checkbook holds %d accounts, want the one that was backed up", len(accounts))
	}
	reg, err := transaction.LoadRegister(t.Context(), store, accounts[0])
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if got := reg.Ending.Decimal(); got != "3728.27" {
		t.Errorf("ending balance in the restored checkbook = %s, want 3728.27", got)
	}
}

// TestRestoreLeavesTheBackupAlone. Restoring is the one operation that reads a
// backup and writes something; if it altered the original, a household would
// have spent their backup to use it.
func TestRestoreLeavesTheBackupAlone(t *testing.T) {
	_, copied := backedUp(t)

	before, err := os.ReadFile(copied)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if _, err := backup.Restore(t.Context(), copied, filepath.Join(filepath.Dir(copied), "restored.db")); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	after, err := os.ReadFile(copied)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("restoring changed the backup it read")
	}

	// It is still a backup afterwards, so it can be restored again.
	store, err := storage.OpenReadOnly(t.Context(), copied)
	if err != nil {
		t.Fatalf("the backup no longer opens: %v", err)
	}
	defer func() { _ = store.Close() }()
	if !store.IsBackup() {
		t.Error("the backup is no longer stamped as one")
	}
}

// TestRestoreNeverWritesOverAFile. The file somebody would be replacing is very
// often the one that shows what went wrong, and a restore is what they reach for
// at exactly the moment they can least afford to lose it.
func TestRestoreNeverWritesOverAFile(t *testing.T) {
	src, copied := backedUp(t)

	if _, err := backup.Restore(t.Context(), copied, src); !errors.Is(err, backup.ErrDestinationExists) {
		t.Fatalf("Restore over an existing file: %v, want ErrDestinationExists", err)
	}

	// And the file it refused to write over is untouched and still openable.
	store, err := storage.Open(t.Context(), src)
	if err != nil {
		t.Fatalf("the checkbook it refused to overwrite no longer opens: %v", err)
	}
	_ = store.Close()
}

// TestRestoreRefusesWhatItCannotRestore, and leaves nothing behind when it does.
func TestRestoreRefusesWhatItCannotRestore(t *testing.T) {
	_, copied := backedUp(t)
	dir := filepath.Dir(copied)

	foreign := filepath.Join(dir, "foreign.db")
	if err := writeForeignDatabase(t, foreign); err != nil {
		t.Fatalf("build a foreign database: %v", err)
	}

	for _, tt := range []struct {
		name string
		src  string
		dest string
		want error
	}{
		{"no source", "", filepath.Join(dir, "a.db"), backup.ErrMissingSource},
		{"no destination", copied, "", backup.ErrMissingDestination},
		{"a source that is not there", filepath.Join(dir, "nowhere.db"), filepath.Join(dir, "b.db"), backup.ErrNotBackup},
		{"a database another program wrote", foreign, filepath.Join(dir, "c.db"), backup.ErrNotBackup},
		{"a folder that is not there", copied, filepath.Join(dir, "nested", "d.db"), backup.ErrMissingDirectory},
		{"an in-memory database", ":memory:demo", filepath.Join(dir, "e.db"), backup.ErrInMemory},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := backup.Restore(t.Context(), tt.src, tt.dest); !errors.Is(err, tt.want) {
				t.Fatalf("Restore: %v, want %v", err, tt.want)
			}
			if tt.dest != "" {
				if _, err := os.Stat(tt.dest); !os.IsNotExist(err) {
					t.Errorf("a refused restore left %s behind", tt.dest)
				}
			}
		})
	}

	// No working file survived any of them either.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".checkbook-") {
			t.Errorf("a working copy was left behind: %q", e.Name())
		}
	}
}

// TestRestoreFromACheckbook. A checkbook is accepted alongside a backup: copying
// your records to a second file is a reasonable thing to want, and refusing it
// would only send the reader back to the file manager this exists to replace.
func TestRestoreFromACheckbook(t *testing.T) {
	src := seeded(t)
	dest := filepath.Join(filepath.Dir(src), "second.db")

	if _, err := backup.Restore(t.Context(), src, dest); err != nil {
		t.Fatalf("Restore from a checkbook: %v", err)
	}
	store, err := storage.Open(t.Context(), dest)
	if err != nil {
		t.Fatalf("open the copy: %v", err)
	}
	_ = store.Close()
}
