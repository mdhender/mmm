// Copyright (c) 2026 Michael D Henderson.

// Package backup writes a verified copy of a checkbook database.
//
// It knows nothing about HTTP and takes a path rather than a *storage.Store
// (SPECIFICATION.md TS-2), which is what lets the same code back up a checkbook
// that is open and one that has just been closed.
//
// A backup is not a file that was written. BK-5 asks for a copy that is usable
// and verifiable, so Create reopens what it wrote and only then reports success;
// the copy carries a backup's name only once it has opened as a checkbook.
//
// A backup is also not a checkbook. BK-6 asks that it never be opened for
// writing, and the enforcement is a byte in the file's own header: Create stamps
// the copy with storage.BackupAppID, which storage.Open refuses and
// storage.OpenReadOnly accepts. The header travels with the file through a
// rename, a copy, or a move to another disk, which the timestamp in the name
// does not.
//
// That leaves records inside a backup reachable by exactly one route, and
// Restore is it: copy the backup to a new file, stamp the copy as an ordinary
// checkbook, and let it migrate forward. Restore is deliberately the only place
// where an old schema is brought up to date, because it is the only place where
// doing so is not destroying the thing that was kept.
package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/storage"
)

const (
	// ErrMissingSource is returned when Create is called without a database to
	// copy.
	ErrMissingSource = cerrs.Error("missing database path")

	// ErrMissingDirectory is returned when the directory that would hold the
	// backup does not exist. Create never makes directories (ST-6): a mistyped
	// path is reported rather than built.
	ErrMissingDirectory = cerrs.Error("backup directory does not exist")

	// ErrInMemory is returned when the database being copied is held in memory.
	// There is nothing on disk to copy, and writing the sample household into
	// somebody's folder under a backup's name would be a lie.
	ErrInMemory = cerrs.Error("an in-memory database cannot be backed up")

	// ErrNameInUse is returned when a free name could not be found for the copy.
	// Create never writes over an existing backup.
	ErrNameInUse = cerrs.Error("a backup already exists under that name")

	// ErrNotVerified is returned when the copy was written but would not reopen
	// as a checkbook (BK-5). The unusable copy is removed.
	ErrNotVerified = cerrs.Error("the copy could not be reopened as a checkbook")

	// ErrMissingDestination is returned when Restore is called without a name to
	// write the restored checkbook under.
	ErrMissingDestination = cerrs.Error("missing destination path")

	// ErrDestinationExists is returned when Restore is asked to write over a
	// file that is already there. It never does. A restore is what somebody
	// reaches for after something went wrong, and the file they would be writing
	// over is quite often the evidence of what it was.
	ErrDestinationExists = cerrs.Error("a file already exists at that path")

	// ErrNotBackup is returned when Restore is handed a file this program did
	// not write.
	ErrNotBackup = cerrs.Error("file is not a checkbook or a backup of one")
)

// memoryPrefix is what storage.Store reports as the path of a database held in
// memory. It is not a filename and there is nothing behind it to copy.
const memoryPrefix = ":memory:"

// nameLayout is the timestamp in a backup's filename. It is the local calendar
// clock, not UTC: the name is read by the household, in the room the machine is
// in, and sorting is already handled by the fixed width.
const nameLayout = "20060102-150405"

// Result describes the copy that was written: a backup by Create, a restored
// checkbook by Restore.
type Result struct {
	// Path is the file that now holds the copy.
	Path string

	// Bytes is its size. A copy is compacted by VACUUM, so this is routinely
	// smaller than the database it came from, which is worth showing rather
	// than hiding.
	Bytes int64
}

// Create writes a timestamped copy of the database at src into dir.
//
// The copy is made with VACUUM INTO on a connection this package opens itself,
// never one borrowed from a Store's pool: the checkbook may well be open and
// serving pages while this runs, and VACUUM cannot run inside a transaction.
//
// Nothing is written under a backup's name until the copy has been reopened and
// found to be a checkbook (BK-5), and an existing backup is never written over.
func Create(ctx context.Context, src, dir string) (Result, error) {
	if src == "" {
		return Result{}, ErrMissingSource
	}
	if len(src) >= len(memoryPrefix) && src[:len(memoryPrefix)] == memoryPrefix {
		return Result{}, fmt.Errorf("%s: %w", src, ErrInMemory)
	}
	if dir == "" {
		dir = filepath.Dir(src)
	}
	// Checked before opening anything. SQLite would fail on a missing directory
	// too, but with a message that says nothing about which part of the path was
	// wrong.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("%s: %w", dir, ErrMissingDirectory)
	}

	dest, err := freeName(dir, time.Now())
	if err != nil {
		return Result{}, err
	}

	// The copy is written under a name of its own and renamed into place at the
	// end, so an interrupted or unverifiable copy never sits in the folder
	// looking like a backup.
	tmp, err := tempName(dir, "backup")
	if err != nil {
		return Result{}, err
	}

	if err := vacuumInto(ctx, src, tmp); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}

	// BK-6. VACUUM INTO copies the header along with the records, so up to this
	// line the copy is a perfectly openable checkbook and nothing but its name
	// says otherwise. Stamping it is what makes it a backup rather than a second
	// checkbook somebody could start typing into by mistake.
	if err := setApplicationID(ctx, tmp, storage.BackupAppID); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}

	// BK-5. A file that will not open is not a backup, and finding that out now
	// is the whole point of doing it here rather than the day it is needed.
	if err := verify(ctx, tmp); err != nil {
		_ = os.Remove(tmp)
		return Result{}, err
	}

	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return Result{}, fmt.Errorf("name the backup %s: %w", dest, err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		return Result{}, fmt.Errorf("measure the backup %s: %w", dest, err)
	}
	return Result{Path: dest, Bytes: info.Size()}, nil
}

// vacuumInto copies the database at src to dest.
//
// VACUUM INTO fails inside a transaction ("cannot VACUUM from within a
// transaction"), so it must never be wrapped in sqlitex.Transaction or Save --
// which also rules out ExecuteScript, since that opens a savepoint. It also
// fails if dest already exists, which is a guard rather than a nuisance.
//
// The destination is bound as a parameter. Pasting a path into the SQL would be
// an injection hazard, and a path is exactly the kind of text that carries
// quotes.
func vacuumInto(ctx context.Context, src, dest string) error {
	// Read-only, and without OpenURI: src is a filesystem path, and a "?" in a
	// folder name must stay part of the name rather than becoming a parameter.
	// Without OpenCreate a missing source is reported instead of created.
	conn, err := sqlite.OpenConn(src, sqlite.OpenReadOnly)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer conn.Close()

	conn.SetInterrupt(ctx.Done())

	err = sqlitex.ExecuteTransient(conn, `VACUUM INTO ?;`, &sqlitex.ExecOptions{
		Args: []any{dest},
	})
	if err != nil {
		return fmt.Errorf("copy %s to %s: %w", src, dest, err)
	}
	return nil
}

// verify reopens the copy as a checkbook and reads it back (BK-5).
//
// Read-only, and deliberately so. storage.Open would migrate the copy and
// convert it to WAL, which would leave the backup differing from what VACUUM
// INTO wrote before it had ever been used -- and the point of a backup is that
// it is the thing that was taken. OpenReadOnly makes the same checks that
// matter here: that it is a SQLite database, that its application_id says it is
// one of ours, and that its schema is the one this build understands.
//
// A quick_check on top is what turns "it opened" into "it is usable". It reads
// the structure of every table and index, which is the difference between a file
// that has a valid header and a file that has the household's records in it.
//
// Two further things are asserted rather than assumed, because both are silent
// when they go wrong. That the stamp took, since a backup still carrying AppID
// is one somebody can open and type into (BK-6). And that the file is not in WAL
// mode, since a backup is meant to be one file the household can copy to another
// disk, and a WAL-mode database is not one file until it has been checkpointed.
// VACUUM INTO produces a rollback-journal database and setApplicationID is
// opened so as not to change that, so this is a guard on both of them.
func verify(ctx context.Context, path string) error {
	store, err := storage.OpenReadOnly(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	defer func() { _ = store.Close() }()

	if !store.IsBackup() {
		return fmt.Errorf("%w: the copy is not stamped as a backup", ErrNotVerified)
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	defer store.Put(conn)

	var result string
	err = sqlitex.ExecuteTransient(conn, `PRAGMA quick_check;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			result = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	if result != "ok" {
		return fmt.Errorf("%w: %s", ErrNotVerified, result)
	}

	mode, err := journalMode(conn)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotVerified, err)
	}
	if strings.EqualFold(mode, "wal") {
		return fmt.Errorf("%w: the copy is in wal mode, so it is not one self-contained file", ErrNotVerified)
	}
	return nil
}

// journalMode reads a connection's journal mode. On a read-only connection this
// reports what the file itself says, which is what verify is asking about.
func journalMode(conn *sqlite.Conn) (string, error) {
	var mode string
	err := sqlitex.ExecuteTransient(conn, `PRAGMA journal_mode;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			mode = stmt.ColumnText(0)
			return nil
		},
	})
	if err != nil {
		return "", fmt.Errorf("read the journal mode: %w", err)
	}
	return mode, nil
}

// setApplicationID writes id into the header of the database at path.
//
// This is the one write either operation makes to the file it is producing, and
// it is made to a working copy that has not yet earned its name -- never to the
// source, and never to a file the household would recognize.
//
// The flags are exactly OpenReadWrite: no OpenCreate, so a missing file is
// reported rather than made, and no OpenWAL, so a rollback-journal database
// stays one. sqlite.OpenConn applies WAL among its defaults when no flags are
// given, and converting the copy would defeat half of what a backup is for.
func setApplicationID(ctx context.Context, path string, id int32) error {
	conn, err := sqlite.OpenConn(path, sqlite.OpenReadWrite)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer conn.Close()

	conn.SetInterrupt(ctx.Done())

	// A pragma argument cannot be bound -- SQLite parses it before parameters
	// exist -- so the statement is built. The rule that says never paste a value
	// into SQL is about values from outside the program; id is an int32 constant
	// declared in storage, formatted as a decimal integer, and there is no
	// spelling of it that could be anything else.
	set := `PRAGMA application_id = ` + strconv.FormatInt(int64(id), 10) + `;`
	if err := sqlitex.ExecuteTransient(conn, set, nil); err != nil {
		return fmt.Errorf("stamp %s: %w", path, err)
	}

	// Read back rather than assume, the way storage verifies its own pragmas. A
	// header field that did not take is silent, and the failure it would leave
	// behind is a backup that opens for writing.
	var got int64
	err = sqlitex.ExecuteTransient(conn, `PRAGMA application_id;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			got = stmt.ColumnInt64(0)
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("read back the application id of %s: %w", path, err)
	}
	if int32(got) != id {
		return fmt.Errorf("stamp %s: application_id is %d, not %d", path, got, id)
	}
	return nil
}

// freeName picks the name the backup will carry.
func freeName(dir string, now time.Time) (string, error) {
	return freeStamped(dir, backupPrefix, now)
}

// tempName is where a copy is written before it has earned its name. kind says
// which operation is producing it, so a folder interrupted mid-restore does not
// hold a file calling itself a backup.
//
// The leading dot keeps a half-written file out of the way on the platforms that
// hide such names, and the token keeps two operations at once from colliding.
func tempName(dir, kind string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("name the working copy: %w", err)
	}
	return filepath.Join(dir, ".checkbook-"+kind+"-"+hex.EncodeToString(b[:])+".tmp"), nil
}

// removeWorkingCopy deletes a working file and anything SQLite left beside it.
//
// A -wal and a -shm appear whenever a database is opened read-write in WAL mode,
// which is what verifying a restore does, and a clean close removes them. This
// is for the paths where the close was not clean: an orphaned -wal outliving the
// file it belongs to is confusing at best, and would be adopted by the next file
// to take that name at worst.
func removeWorkingCopy(path string) {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Remove(name)
	}
}
