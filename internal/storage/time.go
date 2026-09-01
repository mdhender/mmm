// Copyright (c) 2026 Michael D Henderson.

package storage

import (
	"fmt"
	"time"

	"zombiezen.com/go/sqlite"

	"github.com/mdhender/mmm/internal/cerrs"
)

const (
	// ErrInvalidTimestamp is returned when a stored timestamp is not in
	// TimeLayout.
	ErrInvalidTimestamp = cerrs.Error("invalid timestamp")
)

// TimeLayout is the on-disk format for an instant: RFC 3339 in UTC, to
// microsecond precision, with a literal Z.
//
// Everything about it is chosen so the text sorts chronologically as a string:
// fixed width, most significant field first, and a single timezone. Storing a
// local time, or a mixture of offsets, would break ORDER BY and every range
// query silently (SPECIFICATION.md ST-7).
//
// Microseconds rather than nanoseconds because that is ample for a checkbook and
// keeps the column narrow; the precision also bounds NextUpdatedAt.
const TimeLayout = "2006-01-02T15:04:05.000000Z"

// nowSQL produces TimeLayout from SQLite. strftime %f yields seconds with three
// decimals, so three zeros are appended to reach microseconds. SQLite's 'now' is
// already UTC.
const nowSQL = `strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000Z'`

// FormatTime renders t for storage. The value is converted to UTC first, so a
// caller may pass any location.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// ParseTime reads a stored timestamp. The result is always in UTC.
//
// Parsing is strict: a value carrying an offset other than Z, or lacking
// microseconds, is rejected rather than quietly reinterpreted.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", ErrInvalidTimestamp, s)
	}
	return t.UTC(), nil
}

// BindTime binds t to a named parameter in TimeLayout.
func BindTime(stmt *sqlite.Stmt, param string, t time.Time) {
	stmt.SetText(param, FormatTime(t))
}

// ColumnTime reads a named column as an instant in UTC.
func ColumnTime(stmt *sqlite.Stmt, col string) (time.Time, error) {
	t, err := ParseTime(stmt.GetText(col))
	if err != nil {
		return time.Time{}, fmt.Errorf("column %s: %w", col, err)
	}
	return t, nil
}

// NextUpdatedAt returns the updated_at to write for a record whose current
// updated_at is prev, given the current time now.
//
// updated_at doubles as the optimistic concurrency token (SPECIFICATION.md
// CO-3), and a token only works if it changes on every write. A wall clock does
// not guarantee that: two edits inside the same microsecond, or a clock stepped
// backwards by NTP, would produce a token equal to the one already stored, and
// the next compare-and-set would then succeed against stale data — exactly the
// silent overwrite CO-3 forbids.
//
// So the value is the later of now and one microsecond past prev, making it
// strictly increasing per record while still being an honest timestamp. Both are
// truncated to the stored precision first, because a difference finer than
// TimeLayout records would vanish on write.
func NextUpdatedAt(prev, now time.Time) time.Time {
	prev = prev.UTC().Truncate(time.Microsecond)
	next := now.UTC().Truncate(time.Microsecond)
	if !next.After(prev) {
		next = prev.Add(time.Microsecond)
	}
	return next
}
