// Copyright (c) 2026 Michael D Henderson.

package storage_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

// TestFormatTimeNormalizesToUTC confirms an instant is stored in UTC whatever
// location the caller hands over, so the column holds one timezone (ST-7).
func TestFormatTimeNormalizesToUTC(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("timezone database unavailable: %v", err)
	}

	// The same instant, named in three locations.
	instant := time.Date(2026, 8, 29, 16, 30, 0, 0, time.UTC)
	for _, loc := range []*time.Location{time.UTC, ny, kolkata} {
		if got, want := storage.FormatTime(instant.In(loc)), "2026-08-29T16:30:00.000000Z"; got != want {
			t.Errorf("FormatTime in %s = %q, want %q", loc, got, want)
		}
	}
}

func TestParseTimeRoundTrip(t *testing.T) {
	want := time.Date(2026, 8, 29, 16, 30, 0, 123456000, time.UTC)
	got, err := storage.ParseTime(storage.FormatTime(want))
	if err != nil {
		t.Fatalf("ParseTime: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %v, want %v", got, want)
	}
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
}

// TestParseTimeIsStrict confirms a value that is not in TimeLayout is rejected
// rather than quietly reinterpreted in some other zone.
func TestParseTimeIsStrict(t *testing.T) {
	for _, s := range []string{
		"",
		"2026-08-29",
		"2026-08-29T16:30:00Z",            // no microseconds
		"2026-08-29T16:30:00.000000+0530", // an offset that is not Z
		"2026-08-29 16:30:00.000000Z",     // space instead of T
		"not a time",
	} {
		if _, err := storage.ParseTime(s); !errors.Is(err, storage.ErrInvalidTimestamp) {
			t.Errorf("ParseTime(%q) = %v, want ErrInvalidTimestamp", s, err)
		}
	}
}

// TestFormatTimeSortsChronologically confirms the stored text orders the same
// way the instants do, which is what lets ORDER BY and range queries work on a
// TEXT column.
func TestFormatTimeSortsChronologically(t *testing.T) {
	base := time.Date(2026, 8, 29, 16, 30, 0, 0, time.UTC)
	instants := []time.Time{
		base.Add(-24 * time.Hour),
		base.Add(-time.Second),
		base,
		base.Add(time.Microsecond),
		base.Add(time.Hour),
		base.AddDate(1, 0, 0),
	}

	formatted := make([]string, len(instants))
	for i, in := range instants {
		formatted[i] = storage.FormatTime(in)
	}
	if !sort.StringsAreSorted(formatted) {
		t.Fatalf("formatted timestamps do not sort chronologically: %q", formatted)
	}
}

// TestNextUpdatedAtAlwaysAdvances is the core guarantee behind using a timestamp
// as a concurrency token: it must differ from the value already stored, even
// when the clock does not move or steps backwards.
func TestNextUpdatedAtAlwaysAdvances(t *testing.T) {
	prev := time.Date(2026, 8, 29, 16, 30, 0, 123456000, time.UTC)

	for _, tt := range []struct {
		name string
		now  time.Time
	}{
		{name: "clock advanced", now: prev.Add(time.Second)},
		{name: "clock identical", now: prev},
		{name: "clock stepped backwards", now: prev.Add(-time.Hour)},
		{name: "difference finer than storage precision", now: prev.Add(time.Nanosecond)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			next := storage.NextUpdatedAt(prev, tt.now)
			if !next.After(prev) {
				t.Fatalf("NextUpdatedAt = %v, not after prev %v", next, prev)
			}
			// It must also survive the round trip through storage: an advance
			// finer than TimeLayout records would vanish on write.
			if storage.FormatTime(next) == storage.FormatTime(prev) {
				t.Fatalf("stored form unchanged: %q", storage.FormatTime(next))
			}
		})
	}
}

// TestNextUpdatedAtRepeatedlyIncreases confirms a burst of edits inside a single
// clock tick still produces distinct, ordered tokens.
func TestNextUpdatedAtRepeatedlyIncreases(t *testing.T) {
	frozen := time.Date(2026, 8, 29, 16, 30, 0, 0, time.UTC)
	prev := frozen

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		next := storage.NextUpdatedAt(prev, frozen)
		s := storage.FormatTime(next)
		if seen[s] {
			t.Fatalf("iteration %d reused token %q", i, s)
		}
		seen[s] = true
		prev = next
	}
}

// TestTimeColumnRoundTrip confirms BindTime and ColumnTime agree through SQLite.
func TestTimeColumnRoundTrip(t *testing.T) {
	s := openMemory(t, memoryName(t))
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	want := time.Date(2026, 8, 29, 16, 30, 0, 123456000, time.UTC)

	stmt, _, err := c.PrepareTransient(
		`INSERT INTO categories (name, created_at, updated_at)
		 VALUES ('Groceries', $created_at, $updated_at);`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	storage.BindTime(stmt, "$created_at", want)
	storage.BindTime(stmt, "$updated_at", want)
	if _, err := stmt.Step(); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := stmt.Finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	sel, _, err := c.PrepareTransient(`SELECT created_at FROM categories WHERE name = 'Groceries';`)
	if err != nil {
		t.Fatalf("prepare select: %v", err)
	}
	defer sel.Finalize()
	if hasRow, err := sel.Step(); err != nil || !hasRow {
		t.Fatalf("select: hasRow=%v err=%v", hasRow, err)
	}
	got, err := storage.ColumnTime(sel, "created_at")
	if err != nil {
		t.Fatalf("ColumnTime: %v", err)
	}
	if !got.Equal(want) {
		t.Fatalf("round trip = %v, want %v", got, want)
	}
}

// TestMigrationAddsTimestampColumns confirms migration 2 reached every table
// that has a concurrency token, and deliberately skipped splits.
func TestMigrationAddsTimestampColumns(t *testing.T) {
	s := openMemory(t, memoryName(t))
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	for _, table := range []string{"accounts", "transactions", "categories", "reconciliations"} {
		for _, col := range []string{"created_at", "updated_at"} {
			q := `SELECT count(*) FROM pragma_table_info('` + table + `') WHERE name = '` + col + `';`
			if got := queryInt64(t, c, q); got != 1 {
				t.Errorf("%s.%s: found %d, want 1", table, col, got)
			}
		}
	}

	// splits are guarded by their transaction, not independently.
	q := `SELECT count(*) FROM pragma_table_info('splits') WHERE name = 'updated_at';`
	if got := queryInt64(t, c, q); got != 0 {
		t.Errorf("splits.updated_at exists; the transaction is the aggregate root")
	}
}

// TestOptimisticConcurrency demonstrates the CO-3 pattern end to end: two tabs
// read the same transaction, both save, and the second save must not silently
// discard the first.
func TestOptimisticConcurrency(t *testing.T) {
	s := openMemory(t, memoryName(t))
	ctx := context.Background()
	c, err := s.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	now := time.Date(2026, 8, 29, 16, 30, 0, 0, time.UTC)
	accountID := insertAccount(t, c, "Checking", money.USD)

	err = sqlitex.ExecuteTransient(c,
		`INSERT INTO transactions (account_id, date, payee, amount, created_at, updated_at)
		 VALUES (?, '2026-08-29', 'Felipe Motta', -3642, ?, ?);`,
		&sqlitex.ExecOptions{Args: []any{accountID, storage.FormatTime(now), storage.FormatTime(now)}})
	if err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	id := c.LastInsertRowID()

	// Both tabs load the record and hold the same token.
	tabA := readToken(t, c, id)
	tabB := readToken(t, c, id)
	if !tabA.Equal(tabB) {
		t.Fatalf("tabs read different tokens: %v and %v", tabA, tabB)
	}

	// Tab A saves first and succeeds.
	changed := updateWithToken(t, c, id, "Riba Smith", tabA, storage.NextUpdatedAt(tabA, now))
	if changed != 1 {
		t.Fatalf("first save changed %d rows, want 1", changed)
	}

	// Tab B saves against the token it read, which is now stale. It must affect
	// no rows rather than overwrite tab A.
	changed = updateWithToken(t, c, id, "Cable & Wireless", tabB, storage.NextUpdatedAt(tabB, now))
	if changed != 0 {
		t.Fatalf("stale save changed %d rows, want 0 (it silently overwrote the first edit)", changed)
	}

	// Tab A's edit stands.
	var payee string
	err = sqlitex.ExecuteTransient(c, `SELECT payee FROM transactions WHERE id = ?;`,
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				payee = stmt.ColumnText(0)
				return nil
			},
		})
	if err != nil {
		t.Fatalf("select payee: %v", err)
	}
	if payee != "Riba Smith" {
		t.Fatalf("payee = %q, want %q", payee, "Riba Smith")
	}

	// After re-reading, tab B's retry succeeds.
	fresh := readToken(t, c, id)
	changed = updateWithToken(t, c, id, "Cable & Wireless", fresh, storage.NextUpdatedAt(fresh, now))
	if changed != 1 {
		t.Fatalf("retry after re-read changed %d rows, want 1", changed)
	}
}

// readToken returns a transaction's current updated_at.
func readToken(t *testing.T, c *sqlite.Conn, id int64) time.Time {
	t.Helper()
	var token time.Time
	err := sqlitex.ExecuteTransient(c, `SELECT updated_at FROM transactions WHERE id = ?;`,
		&sqlitex.ExecOptions{
			Args: []any{id},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				var err error
				token, err = storage.ColumnTime(stmt, "updated_at")
				return err
			},
		})
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	return token
}

// updateWithToken performs the compare-and-set and reports rows changed.
func updateWithToken(t *testing.T, c *sqlite.Conn, id int64, payee string, expected, next time.Time) int {
	t.Helper()
	err := sqlitex.ExecuteTransient(c,
		`UPDATE transactions SET payee = ?, updated_at = ?
		 WHERE id = ? AND updated_at = ?;`,
		&sqlitex.ExecOptions{Args: []any{
			payee, storage.FormatTime(next), id, storage.FormatTime(expected),
		}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	return c.Changes()
}

// TestTimestampColumnsRejectMalformed confirms the CHECK constraints added when
// the tables were rebuilt: a column that is meant to hold a UTC instant must not
// accept a local time, a bare date, or an offset other than Z (ST-7).
func TestTimestampColumnsRejectMalformed(t *testing.T) {
	s := openMemory(t, memoryName(t))
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	for _, stamp := range []string{
		"2026-08-29",                       // a date, not an instant
		"2026-08-29T16:30:00Z",             // no microseconds
		"2026-08-29T16:30:00.000000",       // no zone at all
		"2026-08-29T16:30:00.000000+05:30", // an offset that is not UTC
		"2026-08-29 16:30:00.000000Z",      // space instead of T
		"",
	} {
		err := sqlitex.ExecuteTransient(c,
			`INSERT INTO categories (name, created_at, updated_at) VALUES (?, ?, ?);`,
			&sqlitex.ExecOptions{Args: []any{"cat-" + stamp, stamp, stamp}})
		if err == nil {
			t.Errorf("stored %q in a timestamp column", stamp)
		}
	}
}

// TestTimestampColumnsDefault confirms an insert that omits the timestamps still
// gets a valid, well-formed instant rather than failing or storing the epoch.
func TestTimestampColumnsDefault(t *testing.T) {
	s := openMemory(t, memoryName(t))
	c, err := s.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer s.Put(c)

	before := time.Now().UTC().Add(-time.Minute)
	if err := sqlitex.ExecuteTransient(c,
		`INSERT INTO categories (name) VALUES ('Groceries');`, nil); err != nil {
		t.Fatalf("insert: %v", err)
	}

	sel, _, err := c.PrepareTransient(`SELECT created_at, updated_at FROM categories WHERE name = 'Groceries';`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer sel.Finalize()
	if hasRow, err := sel.Step(); err != nil || !hasRow {
		t.Fatalf("select: hasRow=%v err=%v", hasRow, err)
	}
	for _, col := range []string{"created_at", "updated_at"} {
		got, err := storage.ColumnTime(sel, col)
		if err != nil {
			t.Fatalf("%s: %v", col, err)
		}
		if got.Before(before) {
			t.Errorf("%s = %v, want a recent instant (default did not apply)", col, got)
		}
	}
}
