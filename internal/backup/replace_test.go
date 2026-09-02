// Copyright (c) 2026 Michael D Henderson.

package backup_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
)

// restoredCopy makes the file a swap puts in place: a real backup, restored to
// the name Replace expects to find it under, in the checkbook's own folder.
func restoredCopy(t *testing.T, from, dir string) string {
	t.Helper()

	name, err := backup.RestoredName(dir, time.Now())
	if err != nil {
		t.Fatalf("RestoredName: %v", err)
	}
	if _, err := backup.Restore(t.Context(), from, name); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return name
}

// fingerprint hashes every file in dir, so a test can assert that the only thing
// an operation changed is which name a file is under.
func fingerprint(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	sums := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name(), err)
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		_ = f.Close()
		sums[entry.Name()] = hex.EncodeToString(h.Sum(nil))
	}
	return sums
}

// contents is the multiset of file bodies in a folder: what is there, ignoring
// what it is called.
func contents(sums map[string]string) []string {
	var out []string
	for _, sum := range sums {
		out = append(out, sum)
	}
	sort.Strings(out)
	return out
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return body
}

// TestReplacePutsTheRestoredFileInPlaceAndKeepsTheOld is the swap itself: one
// press ends with the restored records at the checkbook's name and the checkbook
// that was there still on disk, named, and openable.
func TestReplacePutsTheRestoredFileInPlaceAndKeepsTheOld(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)

	before := mustRead(t, src)

	kept, err := backup.Replace(restored, src)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if kept == "" {
		t.Fatal("Replace kept nothing though there was a checkbook there")
	}
	if !backup.ValidReplacedName(filepath.Base(kept)) {
		t.Errorf("the kept file is named %q, which is not a name this program writes", filepath.Base(kept))
	}
	if filepath.Dir(kept) != dir {
		t.Errorf("the kept file is in %s, want the checkbook's own folder", filepath.Dir(kept))
	}
	if _, err := os.Stat(restored); !os.IsNotExist(err) {
		t.Errorf("%s is still there; it should have taken the checkbook's name", restored)
	}

	// The file that was displaced is byte for byte what it was, and it carries
	// AppID rather than BackupAppID -- so it is openable directly, which is a
	// shorter road back than a file that must itself be restored (BK-1).
	if got := mustRead(t, kept); string(got) != string(before) {
		t.Error("the kept checkbook is not the file that was there")
	}
	if appID, err := storage.ApplicationID(kept); err != nil || appID != storage.AppID {
		t.Errorf("the kept file has application_id %d (%v), want a checkbook's", appID, err)
	}

	// And what is at the checkbook's name now opens as one.
	store, err := storage.Open(t.Context(), src)
	if err != nil {
		t.Fatalf("the restored checkbook does not open: %v", err)
	}
	_ = store.Close()
}

// TestReplaceMovesTheWalWithTheDatabase. SQLite binds a write-ahead log to its
// database by filename, so the pair has to travel together or the records in the
// log are silently orphaned.
func TestReplaceMovesTheWalWithTheDatabase(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)

	// A log and an index beside the checkbook, as a process killed mid-write
	// leaves them.
	if err := os.WriteFile(src+"-wal", []byte("committed frames"), 0o644); err != nil {
		t.Fatalf("write the -wal: %v", err)
	}
	if err := os.WriteFile(src+"-shm", []byte("index"), 0o644); err != nil {
		t.Fatalf("write the -shm: %v", err)
	}

	kept, err := backup.Replace(restored, src)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := mustRead(t, kept+"-wal"); string(got) != "committed frames" {
		t.Errorf("the -wal did not travel with the database it belongs to: %q", got)
	}
}

// TestReplaceLeavesNoSidecarBesideTheNewCheckbook. A -wal left at the checkbook's
// name would be adopted by the file that has just taken it, and it holds another
// database's records.
func TestReplaceLeavesNoSidecarBesideTheNewCheckbook(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)

	if err := os.WriteFile(src+"-wal", []byte("committed frames"), 0o644); err != nil {
		t.Fatalf("write the -wal: %v", err)
	}
	if err := os.WriteFile(src+"-shm", []byte("index"), 0o644); err != nil {
		t.Fatalf("write the -shm: %v", err)
	}

	if _, err := backup.Replace(restored, src); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	for _, sidecar := range []string{src + "-wal", src + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("%s was left beside the restored checkbook", filepath.Base(sidecar))
		}
	}
}

// TestReplaceWithNoCheckbookThere is the emergency: -db never opened, or the
// file is gone entirely. An absent checkbook is not an error, and there is
// nothing to keep.
func TestReplaceWithNoCheckbookThere(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)

	if err := os.Remove(src); err != nil {
		t.Fatalf("remove the checkbook: %v", err)
	}

	kept, err := backup.Replace(restored, src)
	if err != nil {
		t.Fatalf("Replace with nothing at the checkbook's name: %v", err)
	}
	if kept != "" {
		t.Errorf("Replace says it kept %q, though there was nothing there", kept)
	}
	store, err := storage.Open(t.Context(), src)
	if err != nil {
		t.Fatalf("the restored checkbook does not open: %v", err)
	}
	_ = store.Close()
}

// TestReplaceNeverDeletesAnything hashes the folder before and after, so the
// only thing that may have changed is which name a body is under.
func TestReplaceNeverDeletesAnything(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)
	if err := os.WriteFile(src+"-wal", []byte("committed frames"), 0o644); err != nil {
		t.Fatalf("write the -wal: %v", err)
	}

	before := fingerprint(t, dir)
	if _, err := backup.Replace(restored, src); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	after := fingerprint(t, dir)

	if len(before) != len(after) {
		t.Fatalf("the folder held %d files and now holds %d", len(before), len(after))
	}
	got, want := contents(after), contents(before)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the contents of the folder changed:\n before %v\n  after %v", want, got)
		}
	}
}

// TestReplaceRefusesWhenTheAsideCannotBeMade. Nothing moved, so the message is
// the plain one: your checkbook is where it was.
func TestReplaceRefusesWhenTheAsideCannotBeMade(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)
	restored := restoredCopy(t, taken, dir)

	before := mustRead(t, src)

	// The checkbook's folder cannot be written to, so no name beside it can be
	// taken. Skipped for root, which is not stopped by a mode bit.
	if os.Geteuid() == 0 {
		t.Skip("running as root, which a read-only folder does not stop")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := backup.Replace(restored, src); err == nil {
		t.Fatal("Replace reported success though nothing in the folder could be renamed")
	} else if !errors.Is(err, backup.ErrCheckbookNotMoved) {
		t.Errorf("err = %v, want %v", err, backup.ErrCheckbookNotMoved)
	}
	if got := mustRead(t, src); string(got) != string(before) {
		t.Error("the checkbook changed though the swap was refused")
	}
}

// TestReplaceRefusesAFileThatIsNotACheckbook. The header check is the last gate
// before a file takes the household's checkbook name, and it is a header check
// rather than a name check for the reason BK-6 gives.
func TestReplaceRefusesAFileThatIsNotACheckbook(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	taken := backupIn(t, src, dir)

	before := mustRead(t, src)

	for _, tt := range []struct {
		name string
		path string
	}{
		{"a backup", taken},
		{"a text file", filepath.Join(dir, "notes.txt")},
		{"nothing at all", filepath.Join(dir, "gone.db")},
	} {
		if tt.name == "a text file" {
			if err := os.WriteFile(tt.path, []byte("not a database"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			kept, err := backup.Replace(tt.path, src)
			if err == nil {
				t.Fatal("Replace accepted it")
			}
			if !errors.Is(err, backup.ErrNotCheckbook) {
				t.Errorf("err = %v, want %v", err, backup.ErrNotCheckbook)
			}
			if kept != "" {
				t.Errorf("Replace named a kept file, %q", kept)
			}
			if got := mustRead(t, src); string(got) != string(before) {
				t.Error("the checkbook changed though the file was refused")
			}
		})
	}
}

// TestReplaceRefusesToPutAFileOverOne. Replace's two arguments must name two
// files: putting one over itself would either do nothing or destroy it, and the
// caller meant neither.
func TestReplaceRefusesToPutAFileOverOne(t *testing.T) {
	src := seeded(t)
	before := mustRead(t, src)

	if _, err := backup.Replace(src, src); err == nil {
		t.Fatal("Replace accepted one file as both arguments")
	} else if !errors.Is(err, backup.ErrSamePath) {
		t.Errorf("err = %v, want %v", err, backup.ErrSamePath)
	}
	if got := mustRead(t, src); string(got) != string(before) {
		t.Error("the checkbook changed though the swap was refused")
	}
}
