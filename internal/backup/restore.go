// Copyright (c) 2026 Michael D Henderson.

package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/storage"
)

// Restore copies the backup at src to dest and makes the copy an ordinary
// checkbook (SPECIFICATION.md BK-4).
//
// This is the only route from the records inside a backup back into use, and it
// exists because BK-6 closed the other one. Before, a household restored by
// copying the file in their file manager and opening it; that worked, and it
// also meant a single mistyped path could open the backup itself for writing,
// migrate it, and leave them with two copies of the same day and no original.
// Doing the copy here costs one press and removes the mistake.
//
// It is also where an old backup is brought forward. Opening one read-only
// refuses an older schema with ErrDatabaseTooOld, deliberately, because
// migrating it in place would rewrite the thing that was kept. Restore has no
// such problem: it migrates the copy. So a backup from any release this program
// can still migrate from is restorable, however old, which is where the promise
// that a file written today can be read years from now actually lives.
//
// dest is never written over. A restore is what somebody reaches for after
// something went wrong, and the file they would be replacing is quite often the
// evidence of what it was.
func Restore(ctx context.Context, src, dest string) (Result, error) {
	if src == "" {
		return Result{}, ErrMissingSource
	}
	if len(src) >= len(memoryPrefix) && src[:len(memoryPrefix)] == memoryPrefix {
		return Result{}, fmt.Errorf("%s: %w", src, ErrInMemory)
	}
	if dest == "" {
		return Result{}, ErrMissingDestination
	}

	// Checked before anything is opened, so a mistyped destination is reported
	// while both files are still untouched.
	dir := filepath.Dir(dest)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Result{}, fmt.Errorf("%s: %w", dir, ErrMissingDirectory)
	}
	if _, err := os.Stat(dest); err == nil {
		return Result{}, fmt.Errorf("%s: %w", dest, ErrDestinationExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("look at %s: %w", dest, err)
	}

	// Asked what it is before anything is done to it. Deliberately only the
	// header: the schema version is not consulted here, because an old backup is
	// exactly what this is for and refusing it would leave the records with no
	// way out at all.
	//
	// A checkbook is accepted alongside a backup. Restoring from one is a
	// reasonable thing to want -- it is how a household copies their records to
	// a second machine -- and refusing it would only send them back to the file
	// manager this exists to replace.
	// Both sentinels are joined rather than one flattened into text, so a caller
	// can tell "there is no file there" from "that file is not one of ours" and
	// give the reader the advice that fits.
	appID, err := storage.ApplicationID(src)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w: %w", src, ErrNotBackup, err)
	}
	if appID != storage.BackupAppID && appID != storage.AppID {
		return Result{}, fmt.Errorf("%s: %w: application_id is %d", src, ErrNotBackup, appID)
	}

	// Written under a name of its own and renamed at the end, so an interrupted
	// or unusable restore never sits at the address the household is about to
	// open.
	tmp, err := tempName(dir, "restore")
	if err != nil {
		return Result{}, err
	}

	if err := vacuumInto(ctx, src, tmp); err != nil {
		removeWorkingCopy(tmp)
		return Result{}, err
	}

	// The copy stops being a backup here. Everything downstream of this line
	// treats it as the household's checkbook, which is the whole point: it is
	// the file they will be typing into tomorrow.
	if err := setApplicationID(ctx, tmp, storage.AppID); err != nil {
		removeWorkingCopy(tmp)
		return Result{}, err
	}

	if err := verifyRestored(ctx, tmp); err != nil {
		removeWorkingCopy(tmp)
		return Result{}, err
	}

	// Verifying opened the copy read-write, which put it in WAL mode, and
	// closing it checkpointed and removed the log. Checked rather than assumed,
	// and checked rather than tidied away: a -wal still holding records is not
	// litter, and deleting one would silently rename a stale database into
	// place. If it is still here, something did not finish and the honest answer
	// is to say so and keep the original.
	for _, sidecar := range []string{tmp + "-wal", tmp + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			removeWorkingCopy(tmp)
			return Result{}, fmt.Errorf("%w: %s was left beside the copy", ErrNotVerified, filepath.Base(sidecar))
		}
	}

	if err := os.Rename(tmp, dest); err != nil {
		removeWorkingCopy(tmp)
		return Result{}, fmt.Errorf("name the restored checkbook %s: %w", dest, err)
	}

	info, err := os.Stat(dest)
	if err != nil {
		return Result{}, fmt.Errorf("measure the restored checkbook %s: %w", dest, err)
	}
	return Result{Path: dest, Bytes: info.Size()}, nil
}

// verifyRestored opens the copy the way the household is about to open it.
//
// storage.Open, not OpenReadOnly, and that is the difference between this and
// verify. Open migrates, which is what brings a backup from an older release up
// to the schema this build understands, and doing it here rather than on the
// reader's first page means a backup too old or too new to use says so now --
// while the working file can still be thrown away and the original is still
// exactly as it was found.
//
// The store is closed before returning, which checkpoints and removes the -wal
// the migration left beside the working file, so what gets renamed into place is
// one file.
func verifyRestored(ctx context.Context, path string) error {
	// Joined, not flattened: a backup from a release newer than this one fails
	// here, and "written by a newer version" is a different piece of advice from
	// "the copy would not open".
	store, err := storage.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotVerified, err)
	}
	defer func() { _ = store.Close() }()

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
	return nil
}
