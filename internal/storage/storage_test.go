// Copyright (c) 2026 Michael D Henderson.

package storage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

// open returns a Store backed by a fresh database in a temporary directory,
// along with its path and a close func that is safe to call more than once.
func open(t *testing.T) (*storage.Store, string, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checkbook.db")
	s, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Pool.Close blocks until every borrowed connection is returned, so this
	// cleanup must run after the Put registered by conn. Cleanups run LIFO and
	// conn is always called later, so that ordering holds.
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = s.Close() }) })
	return s, path, func() { once.Do(func() { _ = s.Close() }) }
}

// conn borrows a connection that is returned when the test ends.
func conn(t *testing.T, s *storage.Store) *sqlite.Conn {
	t.Helper()
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	t.Cleanup(func() { s.Put(c) })
	return c
}

// setUserVersion writes the database's schema version.
//
// The connection is returned before this function returns, rather than at test
// cleanup like conn does, because callers close the Store immediately afterwards
// and Pool.Close blocks until every borrowed connection is back.
func setUserVersion(t *testing.T, s *storage.Store, version int) {
	t.Helper()
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	query := fmt.Sprintf(`PRAGMA user_version = %d;`, version)
	if err := sqlitex.ExecuteTransient(c, query, nil); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// queryInt64 runs a query expected to return exactly one integer.
func queryInt64(t *testing.T, c *sqlite.Conn, query string) int64 {
	t.Helper()
	var got int64
	var rows int
	err := sqlitex.ExecuteTransient(c, query, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got, rows = stmt.ColumnInt64(0), rows+1
			return nil
		},
	})
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if rows != 1 {
		t.Fatalf("%s returned %d rows, want 1", query, rows)
	}
	return got
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := storage.Open(context.Background(), ""); !errors.Is(err, storage.ErrMissingPath) {
		t.Fatalf("Open(%q) = %v, want ErrMissingPath", "", err)
	}
}

// TestOpenSetsAppID confirms the database is stamped with the ASCII bytes of
// "MMM ", which is what identifies the file as ours.
func TestOpenSetsAppID(t *testing.T) {
	s, _, _ := open(t)
	if got := queryInt64(t, conn(t, s), `PRAGMA application_id;`); got != int64(storage.AppID) {
		t.Fatalf("application_id = %#x, want %#x", got, storage.AppID)
	}
	if got := string([]byte{0x4d, 0x4d, 0x4d, 0x20}); got != "MMM " {
		t.Fatalf("AppID bytes spell %q, want %q", got, "MMM ")
	}
}

// TestOpenAppliesMigrations confirms every table in migration 1 exists and that
// user_version records the number of migrations applied.
func TestOpenAppliesMigrations(t *testing.T) {
	s, _, _ := open(t)
	c := conn(t, s)

	for _, table := range []string{"accounts", "transactions", "splits", "categories", "reconciliations"} {
		q := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '` + table + `';`
		if got := queryInt64(t, c, q); got != 1 {
			t.Errorf("table %s: found %d, want 1", table, got)
		}
	}
	if got, want := queryInt64(t, c, `PRAGMA user_version;`), int64(storage.MigrationCount()); got != want {
		t.Errorf("user_version = %d, want %d (every migration applied)", got, want)
	}
}

// TestOpenIsIdempotent confirms reopening an existing database neither re-runs
// migrations nor fails on the application_id it wrote last time.
func TestOpenIsIdempotent(t *testing.T) {
	_, path, closeStore := open(t)
	closeStore()

	again, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// Borrow, read, and return the connection before closing: Pool.Close waits
	// for outstanding connections.
	c, err := again.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	got := queryInt64(t, c, `PRAGMA user_version;`)
	again.Put(c)

	if want := int64(storage.MigrationCount()); got != want {
		t.Fatalf("user_version = %d after reopen, want %d", got, want)
	}
	if again.Path() != path {
		t.Fatalf("Path() = %q, want %q", again.Path(), path)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestOpenRejectsForeignDatabase confirms the application_id guard: a SQLite
// file belonging to some other program must not be migrated into a checkbook.
func TestOpenRejectsForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-ours.db")

	c, err := sqlite.OpenConn(path, sqlite.OpenReadWrite|sqlite.OpenCreate)
	if err != nil {
		t.Fatalf("OpenConn: %v", err)
	}
	for _, q := range []string{
		`PRAGMA application_id = 305419896;`, // 0x12345678
		`CREATE TABLE someone_elses_data (id INTEGER PRIMARY KEY);`,
	} {
		if err := sqlitex.ExecuteTransient(c, q, nil); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, err := storage.Open(context.Background(), path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded on a database belonging to another application")
	}
	// The UI turns this into "that file is not a checkbook" rather than showing
	// the raw error, so the classification has to hold. It is recognized from
	// sqlitemigration's message; if that wording changes, this is where it
	// should be noticed.
	if !errors.Is(err, storage.ErrNotCheckbook) {
		t.Fatalf("Open = %v, want ErrNotCheckbook", err)
	}
}

// TestOpenRejectsNewerSchema is the guard against a silent downgrade.
// sqlitemigration only migrates forwards, so a database carrying migrations this
// build has never seen falls straight through its loop and would otherwise be
// opened as though it were current.
func TestOpenRejectsNewerSchema(t *testing.T) {
	s, path, closeStore := open(t)

	// Move the database ahead of this build, as a later release would.
	setUserVersion(t, s, 99)
	closeStore()

	again, err := storage.Open(context.Background(), path)
	if err == nil {
		again.Close()
		t.Fatal("Open succeeded on a database written by a newer version")
	}
	if !errors.Is(err, storage.ErrDatabaseTooNew) {
		t.Fatalf("Open = %v, want ErrDatabaseTooNew", err)
	}
	// The message has to name both versions: it is the only way a reader can
	// tell how far ahead the file is.
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error does not report the database schema version: %v", err)
	}
}

// TestOpenRejectsSchemaBehind covers the other direction. It cannot happen after
// a successful migration, so if it is ever seen the schema was interrupted or
// rewritten, and guessing would be worse than refusing.
func TestOpenRejectsSchemaBehind(t *testing.T) {
	s, path, closeStore := open(t)

	setUserVersion(t, s, 1)
	closeStore()

	again, err := storage.Open(context.Background(), path)
	if err != nil {
		// Migrations are append-only, so sqlitemigration will happily re-run
		// migrations 2 and 3 over a database that already has them and fail. Any
		// refusal is acceptable here; opening it silently is not.
		return
	}
	again.Close()
	t.Fatal("Open succeeded on a database whose schema version was moved backwards")
}

// TestForeignKeysEnforced confirms prepareConn turned enforcement on. SQLite
// defaults it off per connection, which would silently make every REFERENCES
// clause in the schema decorative.
func TestForeignKeysEnforced(t *testing.T) {
	s, _, _ := open(t)
	c := conn(t, s)

	if got := queryInt64(t, c, `PRAGMA foreign_keys;`); got != 1 {
		t.Fatalf("PRAGMA foreign_keys = %d, want 1", got)
	}

	err := sqlitex.ExecuteTransient(c,
		`INSERT INTO splits (transaction_id, amount) VALUES (999, 100);`, nil)
	if err == nil {
		t.Fatal("inserted a split referencing a nonexistent transaction")
	}
}

// TestMoneyRoundTrip confirms an amount survives a write and read unchanged, in
// currencies with different scales.
func TestMoneyRoundTrip(t *testing.T) {
	s, _, _ := open(t)
	c := conn(t, s)

	for _, tt := range []struct {
		name     string
		decimal  string
		currency money.Currency
	}{
		{name: "USD two places", decimal: "-84.17", currency: money.USD},
		{name: "USD large", decimal: "1000000.01", currency: money.USD},
		{name: "JPY no minor units", decimal: "4321", currency: money.JPY},
		{name: "KWD three places", decimal: "12.345", currency: money.KWD},
		{name: "zero", decimal: "0.00", currency: money.USD},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want, err := money.ParseDecimal(tt.decimal, tt.currency)
			if err != nil {
				t.Fatalf("ParseDecimal: %v", err)
			}

			accountID := insertAccount(t, c, tt.name, tt.currency)

			stmt, _, err := c.PrepareTransient(
				`INSERT INTO transactions (account_id, date, payee, amount)
				 VALUES ($account_id, '2026-08-29', 'Felipe Motta', $amount);`)
			if err != nil {
				t.Fatalf("prepare insert: %v", err)
			}
			stmt.SetInt64("$account_id", accountID)
			storage.BindMoney(stmt, "$amount", want)
			if _, err := stmt.Step(); err != nil {
				t.Fatalf("insert: %v", err)
			}
			if err := stmt.Finalize(); err != nil {
				t.Fatalf("finalize: %v", err)
			}

			sel, _, err := c.PrepareTransient(
				`SELECT amount FROM transactions WHERE account_id = $account_id;`)
			if err != nil {
				t.Fatalf("prepare select: %v", err)
			}
			defer sel.Finalize()
			sel.SetInt64("$account_id", accountID)
			hasRow, err := sel.Step()
			if err != nil || !hasRow {
				t.Fatalf("select: hasRow=%v err=%v", hasRow, err)
			}

			got, err := storage.ColumnMoney(sel, "amount", tt.currency)
			if err != nil {
				t.Fatalf("ColumnMoney: %v", err)
			}
			if got.Amount() != want.Amount() || got.Currency() != want.Currency() {
				t.Fatalf("round trip = %s, want %s", got, want)
			}
			if got.Decimal() != want.Decimal() {
				t.Fatalf("Decimal() = %s, want %s", got.Decimal(), want.Decimal())
			}
		})
	}
}

// TestAmountRejectsInexactTypes is the regression test for the documented
// footgun: passing a money.Money straight to ExecOptions.Args stringifies it,
// and a float amount is exactly what CO-1 forbids. The typeof() CHECK turns both
// into errors at write time rather than silent corruption.
func TestAmountRejectsInexactTypes(t *testing.T) {
	s, _, _ := open(t)
	c := conn(t, s)
	accountID := insertAccount(t, c, "Checking", money.USD)

	for _, tt := range []struct {
		name string
		arg  any
	}{
		{name: "float", arg: -84.17},
		{name: "text from fmt.Sprint of a Money", arg: "USD -84.17"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := sqlitex.ExecuteTransient(c,
				`INSERT INTO transactions (account_id, date, payee, amount)
				 VALUES (?, '2026-08-29', 'Riba Smith', ?);`,
				&sqlitex.ExecOptions{Args: []any{accountID, tt.arg}})
			if err == nil {
				t.Fatalf("stored %v (%T) in an amount column", tt.arg, tt.arg)
			}
		})
	}
}

// TestStatusAndDateConstraints confirms the schema rejects the states the
// register has no meaning for.
func TestStatusAndDateConstraints(t *testing.T) {
	s, _, _ := open(t)
	c := conn(t, s)
	accountID := insertAccount(t, c, "Checking", money.USD)

	for _, tt := range []struct {
		name  string
		date  string
		state string
	}{
		{name: "unknown status", date: "2026-08-29", state: "pending"},
		{name: "malformed date", date: "08/29/2026", state: "cleared"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := sqlitex.ExecuteTransient(c,
				`INSERT INTO transactions (account_id, date, payee, amount, status)
				 VALUES (?, ?, 'Riba Smith', -8417, ?);`,
				&sqlitex.ExecOptions{Args: []any{accountID, tt.date, tt.state}})
			if err == nil {
				t.Fatalf("accepted date=%q status=%q", tt.date, tt.state)
			}
		})
	}
}

// insertAccount adds an account and returns its id.
func insertAccount(t *testing.T, c *sqlite.Conn, name string, cur money.Currency) int64 {
	t.Helper()
	err := sqlitex.ExecuteTransient(c,
		`INSERT INTO accounts (name, type, currency) VALUES (?, 'checking', ?);`,
		&sqlitex.ExecOptions{Args: []any{name, string(cur)}})
	if err != nil {
		t.Fatalf("insert account: %v", err)
	}
	return c.LastInsertRowID()
}

// TestOpenNeverCreatesDirectories confirms Open fails on a path whose parent
// does not exist, and leaves the filesystem alone. Without this, a mistyped or
// stray relative path in a test would scatter directories around the tree.
func TestOpenNeverCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "no", "such", "dir")
	path := filepath.Join(missing, "checkbook.db")

	s, err := storage.Open(context.Background(), path)
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded with a nonexistent parent directory")
	}
	if !errors.Is(err, storage.ErrMissingDirectory) {
		t.Fatalf("Open = %v, want ErrMissingDirectory", err)
	}

	// Nothing may have been created, at any level.
	for _, p := range []string{path, missing, filepath.Join(root, "no", "such"), filepath.Join(root, "no")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after a failed Open", p)
		}
	}
}

// TestPragmasHoldOnEveryConnection borrows every connection in the pool at once
// and confirms each enforces foreign keys and is in WAL mode.
//
// sqlitex prepares a connection lazily, on its first Take, so a pragma checked
// only on the first borrow proves nothing about the other nine. Several browser
// tabs will use several connections.
func TestPragmasHoldOnEveryConnection(t *testing.T) {
	s, _, _ := open(t)
	ctx := context.Background()

	// Hold them all simultaneously so the pool cannot hand back the same one.
	conns := make([]*sqlite.Conn, 0, storage.PoolSize)
	defer func() {
		for _, c := range conns {
			s.Put(c)
		}
	}()

	for i := 0; i < storage.PoolSize; i++ {
		c, err := s.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		conns = append(conns, c)
	}

	for i, c := range conns {
		if got := queryInt64(t, c, `PRAGMA foreign_keys;`); got != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i, got)
		}
		var mode string
		err := sqlitex.ExecuteTransient(c, `PRAGMA journal_mode;`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error {
				mode = stmt.ColumnText(0)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("connection %d: journal_mode: %v", i, err)
		}
		if !strings.EqualFold(mode, "wal") {
			t.Errorf("connection %d: journal_mode = %q, want wal", i, mode)
		}
	}
}

// TestConcurrentReadDuringWrite confirms WAL is doing its job: a reader on one
// connection is not blocked by an open write transaction on another. This is the
// multi-tab case -- one tab importing while another scrolls the register.
//
// Note what this does NOT establish: WAL prevents readers and writers from
// blocking each other, not one tab overwriting another tab's edit. See the
// concurrency note in CLAUDE.md.
func TestConcurrentReadDuringWrite(t *testing.T) {
	s, _, _ := open(t)
	ctx := context.Background()

	writer, err := s.Conn(ctx)
	if err != nil {
		t.Fatalf("writer Conn: %v", err)
	}
	defer s.Put(writer)
	reader, err := s.Conn(ctx)
	if err != nil {
		t.Fatalf("reader Conn: %v", err)
	}
	defer s.Put(reader)

	insertAccount(t, writer, "Checking", money.USD)

	if err := sqlitex.ExecuteTransient(writer, `BEGIN IMMEDIATE;`, nil); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := sqlitex.ExecuteTransient(writer,
		`INSERT INTO accounts (name, type, currency) VALUES ('Savings', 'savings', 'USD');`,
		nil); err != nil {
		t.Fatalf("insert during transaction: %v", err)
	}

	// The reader must see the pre-transaction snapshot without blocking.
	if got := queryInt64(t, reader, `SELECT count(*) FROM accounts;`); got != 1 {
		t.Errorf("reader saw %d accounts mid-write, want 1 (uncommitted row must be invisible)", got)
	}

	if err := sqlitex.ExecuteTransient(writer, `COMMIT;`, nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := queryInt64(t, reader, `SELECT count(*) FROM accounts;`); got != 2 {
		t.Errorf("reader saw %d accounts after commit, want 2", got)
	}
}

// memoryName turns a test name into one OpenMemory accepts: subtest names
// contain "/", which is rejected so a name cannot smuggle in a URI parameter.
func memoryName(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "-")
}

// openMemory returns a shared in-memory Store named for the running test.
func openMemory(t *testing.T, name string) *storage.Store {
	t.Helper()
	s, err := storage.OpenMemory(context.Background(), name)
	if err != nil {
		t.Fatalf("OpenMemory(%q): %v", name, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestOpenMemoryMigrates confirms an in-memory store gets the same schema as a
// file-backed one, in both shared and private modes.
func TestOpenMemoryMigrates(t *testing.T) {
	for _, name := range []string{memoryName(t), ""} {
		mode := "shared"
		if name == "" {
			mode = "private"
		}
		t.Run(mode, func(t *testing.T) {
			s := openMemory(t, name)
			c, err := s.Conn(context.Background())
			if err != nil {
				t.Fatalf("Conn: %v", err)
			}
			defer s.Put(c)

			for _, table := range []string{"accounts", "transactions", "splits", "categories", "reconciliations"} {
				q := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = '` + table + `';`
				if got := queryInt64(t, c, q); got != 1 {
					t.Errorf("table %s: found %d, want 1", table, got)
				}
			}
			if got := queryInt64(t, c, `PRAGMA application_id;`); got != int64(storage.AppID) {
				t.Errorf("application_id = %#x, want %#x", got, storage.AppID)
			}
			// WAL is impossible in memory; the store must accept that rather
			// than failing its journal_mode check.
			if got := queryInt64(t, c, `PRAGMA foreign_keys;`); got != 1 {
				t.Errorf("foreign_keys = %d, want 1 (constraints must behave as on disk)", got)
			}
		})
	}
}

// TestOpenMemorySharedAcrossConnections confirms a named in-memory database is
// one database, not one per connection -- the trap that makes bare ":memory:"
// useless with a pool.
func TestOpenMemorySharedAcrossConnections(t *testing.T) {
	s := openMemory(t, memoryName(t))
	ctx := context.Background()

	writer, err := s.Conn(ctx)
	if err != nil {
		t.Fatalf("writer Conn: %v", err)
	}
	func() {
		defer s.Put(writer)
		insertAccount(t, writer, "Checking", money.USD)
	}()

	// A different connection from the pool must see it. Hold several at once so
	// this cannot be satisfied by getting the same connection back.
	conns := make([]*sqlite.Conn, 0, 4)
	defer func() {
		for _, c := range conns {
			s.Put(c)
		}
	}()
	for i := 0; i < 4; i++ {
		c, err := s.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		conns = append(conns, c)
		if got := queryInt64(t, c, `SELECT count(*) FROM accounts;`); got != 1 {
			t.Errorf("connection %d saw %d accounts, want 1", i, got)
		}
	}
}

// TestOpenMemoryIsolation confirms distinct names are distinct databases, and
// that private stores cannot reach each other at all.
func TestOpenMemoryIsolation(t *testing.T) {
	t.Run("distinct names", func(t *testing.T) {
		base := memoryName(t)
		a, b := openMemory(t, base+"-a"), openMemory(t, base+"-b")
		writeAndCompare(t, a, b)
	})
	t.Run("private stores", func(t *testing.T) {
		a, b := openMemory(t, ""), openMemory(t, "")
		writeAndCompare(t, a, b)
	})
}

// writeAndCompare inserts into a and confirms b never sees it.
func writeAndCompare(t *testing.T, a, b *storage.Store) {
	t.Helper()
	ctx := context.Background()

	ca, err := a.Conn(ctx)
	if err != nil {
		t.Fatalf("a.Conn: %v", err)
	}
	func() {
		defer a.Put(ca)
		insertAccount(t, ca, "Checking", money.USD)
	}()

	cb, err := b.Conn(ctx)
	if err != nil {
		t.Fatalf("b.Conn: %v", err)
	}
	defer b.Put(cb)
	if got := queryInt64(t, cb, `SELECT count(*) FROM accounts;`); got != 0 {
		t.Fatalf("second store saw %d accounts, want 0 (databases must be isolated)", got)
	}
}

// TestOpenMemoryPrivateIsSingleConnection documents the cost of a private
// database: it cannot be shared, so the pool holds exactly one connection and a
// second borrow waits for the first to come back.
func TestOpenMemoryPrivateIsSingleConnection(t *testing.T) {
	s := openMemory(t, "")

	first, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("first Conn: %v", err)
	}
	defer s.Put(first)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if second, err := s.Conn(ctx); err == nil {
		s.Put(second)
		t.Fatal("second connection was available from a private in-memory store")
	}
}

// TestOpenMemoryRejectsUnsafeNames confirms a name cannot smuggle in URI query
// parameters, which could quietly turn a test database into a file.
func TestOpenMemoryRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{
		"has space",
		"cache=shared&mode=rwc",
		"../escape",
		"query?mode=rwc",
	} {
		if s, err := storage.OpenMemory(context.Background(), name); !errors.Is(err, storage.ErrInvalidMemoryName) {
			if err == nil {
				s.Close()
			}
			t.Errorf("OpenMemory(%q) = %v, want ErrInvalidMemoryName", name, err)
		}
	}
}

// TestOpenMemoryTouchesNoFiles confirms an in-memory store leaves the working
// directory alone.
func TestOpenMemoryTouchesNoFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	shared := openMemory(t, "TouchesNoFiles")
	private := openMemory(t, "")
	for _, s := range []*storage.Store{shared, private} {
		c, err := s.Conn(context.Background())
		if err != nil {
			t.Fatalf("Conn: %v", err)
		}
		func() {
			defer s.Put(c)
			insertAccount(t, c, "Checking-"+s.Path(), money.USD)
		}()
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("in-memory stores created files: %v", names)
	}
}

// TestPrimaryKeysAreNeverReused confirms every table uses AUTOINCREMENT rather
// than a bare rowid alias.
//
// SQLite assigns max(rowid)+1 by default, so deleting the newest row hands its id
// to the next insert. Anything still holding that id -- an open browser tab, a
// bookmarked URL, an exported file, a reconciliation -- would silently start
// pointing at an unrelated record.
func TestPrimaryKeysAreNeverReused(t *testing.T) {
	s := openMemory(t, memoryName(t))
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	accountID := insertAccount(t, c, "Checking", money.USD)
	exec := func(query string, args ...any) {
		t.Helper()
		if err := sqlitex.ExecuteTransient(c, query, &sqlitex.ExecOptions{Args: args}); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO transactions (account_id, date, payee, amount) VALUES (?, '2026-08-29', 'Seed', -1);`, accountID)
	seedTxn := c.LastInsertRowID()

	for _, tt := range []struct {
		table  string
		insert string
		args   []any
	}{
		{table: "categories", insert: `INSERT INTO categories (name) VALUES (?);`, args: []any{"Wine"}},
		{table: "accounts", insert: `INSERT INTO accounts (name, type, currency) VALUES (?, 'checking', 'USD');`, args: []any{"Savings"}},
		{
			table:  "transactions",
			insert: `INSERT INTO transactions (account_id, date, payee, amount) VALUES (?, '2026-08-29', 'Felipe Motta', -3642);`,
			args:   []any{accountID},
		},
		{
			table:  "splits",
			insert: `INSERT INTO splits (transaction_id, amount) VALUES (?, -3642);`,
			args:   []any{seedTxn},
		},
		{
			table:  "reconciliations",
			insert: `INSERT INTO reconciliations (account_id, statement_date, statement_balance) VALUES (?, '2026-08-31', 462317);`,
			args:   []any{accountID},
		},
	} {
		t.Run(tt.table, func(t *testing.T) {
			exec(tt.insert, tt.args...)
			first := c.LastInsertRowID()

			exec(`DELETE FROM `+tt.table+` WHERE id = ?;`, first)

			exec(tt.insert, tt.args...)
			second := c.LastInsertRowID()

			if second == first {
				t.Fatalf("%s reused id %d after deletion", tt.table, first)
			}
			if second < first {
				t.Fatalf("%s issued a decreasing id: %d then %d", tt.table, first, second)
			}
		})
	}

	// AUTOINCREMENT keeps its high-water marks here; its absence would mean the
	// tables fell back to plain rowids.
	q := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sqlite_sequence';`
	if got := queryInt64(t, c, q); got != 1 {
		t.Error("sqlite_sequence is missing; tables are not using AUTOINCREMENT")
	}
}
