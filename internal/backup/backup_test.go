// Copyright (c) 2026 Michael D Henderson.

package backup_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// seeded builds a checkbook on disk with one account and one transaction, and
// returns its path. The store is closed before returning, because a backup has
// to work on a file whether or not the program has it open.
func seeded(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "checkbook.db")
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	amount, err := money.ParseDecimal("3812.44", money.USD)
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}
	acct, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD, OpeningBalance: amount,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	spend, err := money.ParseDecimal("-84.17", money.USD)
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}
	if _, err := transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-08-27", Payee: "Riba Smith", Amount: spend,
	}); err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	return path
}

// TestRoundTrip is BK-5 end to end: the copy is opened as a checkbook and the
// register read out of it matches the one it was copied from.
func TestRoundTrip(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)

	res, err := backup.Create(t.Context(), src, dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if filepath.Dir(res.Path) != dir {
		t.Errorf("backup written to %s, want a file in %s", res.Path, dir)
	}
	if base := filepath.Base(res.Path); !strings.HasPrefix(base, "checkbook-") || !strings.HasSuffix(base, ".db") {
		t.Errorf("backup is named %q, want checkbook-YYYYMMDD-HHMMSS.db", base)
	}
	if res.Bytes <= 0 {
		t.Errorf("backup reports %d bytes", res.Bytes)
	}

	// Read-only, because a backup no longer opens any other way (BK-6). What
	// BK-5 asks is that the records are in there and readable, and they are.
	store, err := storage.OpenReadOnly(t.Context(), res.Path)
	if err != nil {
		t.Fatalf("open the backup: %v", err)
	}
	defer func() { _ = store.Close() }()

	if !store.IsBackup() {
		t.Error("the copy is not stamped as a backup")
	}

	accounts, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Name != "Checking" {
		t.Fatalf("backup holds %d accounts, want the one that was copied", len(accounts))
	}
	reg, err := transaction.LoadRegister(t.Context(), store, accounts[0])
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(reg.Entries) != 1 {
		t.Fatalf("backup holds %d transactions, want 1", len(reg.Entries))
	}
	if got := reg.Ending.Decimal(); got != "3728.27" {
		t.Errorf("ending balance in the backup = %s, want 3728.27", got)
	}
}

// TestSourceStaysOpenable: backing up an open checkbook must not disturb it.
func TestBackupWhileOpen(t *testing.T) {
	src := seeded(t)

	store, err := storage.Open(t.Context(), src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, err := backup.Create(t.Context(), src, filepath.Dir(src)); err != nil {
		t.Fatalf("Create with the checkbook open: %v", err)
	}

	if _, err := account.List(t.Context(), store); err != nil {
		t.Errorf("the open checkbook stopped working after a backup: %v", err)
	}
}

// TestInMemoryIsRefused: -demo has nothing on disk to copy, and a file of sample
// data carrying a backup's name in the household's folder would be a lie.
func TestInMemoryIsRefused(t *testing.T) {
	dir := t.TempDir()

	_, err := backup.Create(t.Context(), ":memory:checkbook-demo", dir)
	if !errors.Is(err, backup.ErrInMemory) {
		t.Fatalf("Create on an in-memory database: %v, want ErrInMemory", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused backup left %d files behind", len(entries))
	}
}

// TestMissingDirectoryIsRefused is ST-6: a mistyped path is reported, never
// built.
func TestMissingDirectoryIsRefused(t *testing.T) {
	src := seeded(t)
	missing := filepath.Join(t.TempDir(), "backups")

	_, err := backup.Create(t.Context(), src, missing)
	if !errors.Is(err, backup.ErrMissingDirectory) {
		t.Fatalf("Create into a missing directory: %v, want ErrMissingDirectory", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("the missing directory was created")
	}
}

func TestMissingSourceIsRefused(t *testing.T) {
	dir := t.TempDir()

	if _, err := backup.Create(t.Context(), "", dir); !errors.Is(err, backup.ErrMissingSource) {
		t.Errorf("Create with no source: %v, want ErrMissingSource", err)
	}

	// A source that does not exist must be reported, not created: OpenCreate is
	// deliberately absent from the flags.
	absent := filepath.Join(dir, "nowhere.db")
	if _, err := backup.Create(t.Context(), absent, dir); err == nil {
		t.Error("Create on a database that does not exist succeeded")
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Error("Create made the database it was asked to copy")
	}
}

// TestUnverifiableCopyIsNotNamedABackup is the BK-5 path: a file that will not
// reopen as a checkbook never reaches the folder under a backup's name.
func TestUnverifiableCopyIsNotNamedABackup(t *testing.T) {
	// A SQLite database written by something that is not this program: it copies
	// fine and fails to open as a checkbook, because its application_id is not
	// ours.
	dir := t.TempDir()
	foreign := filepath.Join(dir, "foreign.db")
	if err := writeForeignDatabase(t, foreign); err != nil {
		t.Fatalf("build a foreign database: %v", err)
	}

	_, err := backup.Create(t.Context(), foreign, dir)
	if !errors.Is(err, backup.ErrNotVerified) {
		t.Fatalf("Create from a foreign database: %v, want ErrNotVerified", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "checkbook-") {
			t.Errorf("an unverifiable copy was left named %q", e.Name())
		}
		if strings.HasPrefix(e.Name(), ".checkbook-backup-") {
			t.Errorf("a working copy was left behind: %q", e.Name())
		}
	}
}

// TestSecondBackupDoesNotOverwriteTheFirst: losing a backup to a backup would be
// the worst possible way to lose one, so a name already in use gets a counter.
func TestSecondBackupDoesNotOverwriteTheFirst(t *testing.T) {
	src := seeded(t)
	dir := filepath.Dir(src)

	first, err := backup.Create(t.Context(), src, dir)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := backup.Create(t.Context(), src, dir)
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if first.Path == second.Path {
		t.Fatalf("both backups were written to %s", first.Path)
	}
	for _, p := range []string{first.Path, second.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("backup %s is gone: %v", p, err)
		}
	}
}

// writeForeignDatabase builds a SQLite file belonging to some other program:
// well formed, and not a checkbook, because its application_id is not ours.
func writeForeignDatabase(t *testing.T, path string) error {
	t.Helper()

	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`PRAGMA application_id = 305419896;`, // 0x12345678
		`CREATE TABLE someone_elses_data (id INTEGER PRIMARY KEY);`,
	} {
		if err := sqlitex.ExecuteTransient(conn, q, nil); err != nil {
			conn.Close()
			return err
		}
	}
	return conn.Close()
}
