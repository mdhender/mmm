// Copyright (c) 2026 Michael D Henderson.

package backup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mdhender/mmm/internal/backup"
)

// backupIn writes a real backup into dir, taken from src, and returns its path.
func backupIn(t *testing.T, src, dir string) string {
	t.Helper()

	res, err := backup.Create(t.Context(), src, dir)
	if err != nil {
		t.Fatalf("Create into %s: %v", dir, err)
	}
	return res.Path
}

// touch sets a file's modification time, so a test can say which of two copies
// is the newer one rather than hoping the clock separated them.
func touch(t *testing.T, path string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes %s: %v", path, err)
	}
}

// names reduces a listing to the base names it found, in the order it found
// them.
func names(found []backup.Backup) []string {
	var out []string
	for _, b := range found {
		out = append(out, filepath.Base(b.Path))
	}
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestFindListsBackupsNewestFirst. The list is a list of moments, and the most
// recent one is what somebody restoring is nearly always after.
func TestFindListsBackupsNewestFirst(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)

	older := backupIn(t, src, dir)
	newer := backupIn(t, src, dir)
	touch(t, older, time.Now().Add(-48*time.Hour))
	touch(t, newer, time.Now().Add(-1*time.Hour))
	// The checkbook is the oldest of the three here, so the order under test is
	// the one the timestamps say rather than the one the directory happened to
	// list.
	touch(t, src, time.Now().Add(-72*time.Hour))

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) < 2 {
		t.Fatalf("Find returned %d files, want at least the two backups: %v", len(found), names(found))
	}
	if found[0].Path != newer {
		t.Errorf("first entry is %s, want the newer backup %s", found[0].Path, newer)
	}
	if found[0].Taken.Before(found[1].Taken) {
		t.Error("the listing is not newest first")
	}
	if !found[0].IsBackup {
		t.Error("a backup is not marked as one")
	}
	if found[0].Bytes <= 0 {
		t.Errorf("the backup reports %d bytes", found[0].Bytes)
	}
	if !filepath.IsAbs(found[0].Path) {
		t.Errorf("path %q is not absolute", found[0].Path)
	}

	// The checkbook itself is offered too. Restoring from one is how a household
	// copies their records to a second machine, and Restore accepts either.
	var checkbooks int
	for _, b := range found {
		if !b.IsBackup {
			checkbooks++
		}
	}
	if checkbooks != 1 {
		t.Errorf("found %d checkbooks beside the backups, want the one", checkbooks)
	}
}

// TestFindConfirmsByHeaderNotByName is BK-6's last sentence as a test: the name
// is a hint for the household, and the header is the enforcement.
func TestFindConfirmsByHeaderNotByName(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)

	// A real backup, renamed to something that looks like anything but one.
	real := backupIn(t, src, dir)
	disguised := filepath.Join(dir, "notes.txt")
	if err := os.Rename(real, disguised); err != nil {
		t.Fatalf("rename the backup: %v", err)
	}

	// And a text file wearing a backup's name.
	impostor := filepath.Join(dir, "checkbook-20260101-000000.db")
	if err := os.WriteFile(impostor, []byte("this is not a database\n"), 0o644); err != nil {
		t.Fatalf("write the impostor: %v", err)
	}

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	got := names(found)
	if !contains(got, "notes.txt") {
		t.Errorf("a backup named notes.txt was not found: %v", got)
	}
	if contains(got, "checkbook-20260101-000000.db") {
		t.Errorf("a text file wearing a backup's name was listed: %v", got)
	}
}

// TestFindLooksInBothPlaces. Backups written before there was a folder are still
// backups, and they are not moved.
func TestFindLooksInBothPlaces(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)

	beside := backupIn(t, src, dir)
	folder := backup.Folder(src)
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatalf("Mkdir %s: %v", folder, err)
	}
	inside := backupIn(t, src, folder)

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	var sawBeside, sawInside bool
	for _, b := range found {
		switch b.Path {
		case beside:
			sawBeside = true
		case inside:
			sawInside = true
		}
	}
	if !sawBeside {
		t.Errorf("the backup beside the checkbook was not found: %v", names(found))
	}
	if !sawInside {
		t.Errorf("the backup in %s was not found: %v", folder, names(found))
	}
}

// TestFindDoesNotMindAMissingBackupsFolder, and creates nothing while finding
// that out (ST-10: a folder is made in answer to an explicit action, never as a
// side effect of listing).
func TestFindDoesNotMindAMissingBackupsFolder(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	backupIn(t, src, dir)

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find with no backups folder: %v", err)
	}
	if len(found) == 0 {
		t.Error("Find returned nothing though there is a backup beside the checkbook")
	}
	if _, err := os.Stat(backup.Folder(src)); !os.IsNotExist(err) {
		t.Errorf("Find created %s", backup.Folder(src))
	}
}

// TestFindSkipsSidecarsAndWorkingCopies. A -wal is not a file anybody restores
// from, and a .checkbook-*.tmp may be half written by an operation running right
// now: neither is opened, and the skipping is by name so that it happens before
// anything is opened at all.
func TestFindSkipsSidecarsAndWorkingCopies(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	real := backupIn(t, src, dir)

	// Copies of a genuine database, so a listing that opened them would find a
	// perfectly good header and list them.
	body, err := os.ReadFile(real)
	if err != nil {
		t.Fatalf("read the backup: %v", err)
	}
	for _, name := range []string{
		"checkbook.db-wal", "checkbook.db-shm", "checkbook.db-journal",
		".checkbook-restore-abc123.tmp",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, got := range names(found) {
		if strings.HasSuffix(got, "-wal") || strings.HasSuffix(got, "-shm") ||
			strings.HasSuffix(got, "-journal") || strings.HasSuffix(got, ".tmp") {
			t.Errorf("Find listed %q", got)
		}
	}
}

// TestFindSkipsDirectories. The backups folder itself is inside the folder being
// read, and a directory is not something to open as a database.
func TestFindSkipsDirectories(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)
	if err := os.Mkdir(backup.Folder(src), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "checkbook-20260101-000000.db"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	found, err := backup.Find(dir)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for _, got := range names(found) {
		if got == "backups" || got == "checkbook-20260101-000000.db" {
			t.Errorf("Find listed the directory %q", got)
		}
	}
}

// TestFindReportsAMissingFolder. The folder being read is the one holding the
// household's records; if it is gone, that is worth saying rather than showing
// an empty list that reads as "you have no backups".
func TestFindReportsAMissingFolder(t *testing.T) {
	if _, err := backup.Find(filepath.Join(t.TempDir(), "nowhere")); err == nil {
		t.Fatal("Find accepted a folder that does not exist")
	}
}

// TestFolderIsBesideTheCheckbook pins the one place the convention is spelled.
func TestFolderIsBesideTheCheckbook(t *testing.T) {
	got := backup.Folder(filepath.Join("home", "records", "checkbook.db"))
	want := filepath.Join("home", "records", "backups")
	if got != want {
		t.Errorf("Folder = %q, want %q", got, want)
	}
}

// TestStampInNameReadsWhatWeWrote. The displayed date comes from the name when
// the name is one of ours, because a copy moved between disks keeps its name and
// loses its modification time.
func TestStampInNameReadsWhatWeWrote(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
		ok   bool
	}{
		{"checkbook-20260902-141530.db", "2026-09-02 14:15:30", true},
		{"checkbook-20260902-141530-2.db", "2026-09-02 14:15:30", true},
		{"checkbook-replaced-20260902-153104.db", "2026-09-02 15:31:04", true},
		{"checkbook-restored-20260902-153104.db", "2026-09-02 15:31:04", true},
		{"notes.txt", "", false},
		{"checkbook.db", "", false},
		{"checkbook-not-a-stamp.db", "", false},
	} {
		got, ok := backup.StampInName(tt.name)
		if ok != tt.ok {
			t.Errorf("StampInName(%q) ok = %v, want %v", tt.name, ok, tt.ok)
			continue
		}
		if ok && got.Format("2006-01-02 15:04:05") != tt.want {
			t.Errorf("StampInName(%q) = %s, want %s", tt.name, got.Format("2006-01-02 15:04:05"), tt.want)
		}
	}
}

// TestValidNamesAreToldApart. Both are put into sentences a reader is meant to
// believe, and "checkbook-replaced-..." is not a backup.
func TestValidNamesAreToldApart(t *testing.T) {
	if !backup.ValidBackupName("checkbook-20260902-141530.db") {
		t.Error("a backup's own name was rejected")
	}
	if backup.ValidBackupName("checkbook-replaced-20260902-153104.db") {
		t.Error("a displaced checkbook was accepted as a backup's name")
	}
	if !backup.ValidReplacedName("checkbook-replaced-20260902-153104.db") {
		t.Error("a displaced checkbook's own name was rejected")
	}
	for _, name := range []string{"", "checkbook.db", "../checkbook-20260902-141530.db", "checkbook-2026-141530.db"} {
		if backup.ValidBackupName(name) || backup.ValidReplacedName(name) {
			t.Errorf("%q was accepted as a name this program wrote", name)
		}
	}
}

// TestRestoredNameIsFree. The restored copy and the file it displaces share a
// stamp, and neither may land on a name that is already taken.
func TestRestoredNameIsFree(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 2, 15, 31, 4, 0, time.Local)

	first, err := backup.RestoredName(dir, now)
	if err != nil {
		t.Fatalf("RestoredName: %v", err)
	}
	if base := filepath.Base(first); base != "checkbook-restored-20260902-153104.db" {
		t.Errorf("name = %q", base)
	}
	if err := os.WriteFile(first, []byte("taken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	second, err := backup.RestoredName(dir, now)
	if err != nil {
		t.Fatalf("RestoredName: %v", err)
	}
	if second == first {
		t.Error("RestoredName handed out a name that is already in use")
	}

	// A -wal at the name counts as the name being taken: it would be adopted by
	// whatever file took it.
	if err := os.WriteFile(second+"-wal", []byte("log"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	third, err := backup.RestoredName(dir, now)
	if err != nil {
		t.Fatalf("RestoredName: %v", err)
	}
	if third == second {
		t.Error("RestoredName picked a name with a -wal beside it")
	}
}
