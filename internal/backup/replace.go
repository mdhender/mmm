// Copyright (c) 2026 Michael D Henderson.

package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/storage"
)

const (
	// ErrNotCheckbook is returned when the file Replace was asked to put in
	// place is not a checkbook. It is the last gate before a file takes the
	// household's checkbook name, and it is a header check rather than a name
	// check for the reason BK-6 gives: the name does not survive a rename and
	// the header does.
	ErrNotCheckbook = cerrs.Error("file is not a checkbook")

	// ErrSamePath is returned when the restored file and the checkbook are one
	// file. Putting it over itself would either do nothing or destroy it, and
	// neither is what the caller meant.
	ErrSamePath = cerrs.Error("the restored file and the checkbook are the same file")

	// ErrCheckbookNotMoved is returned when the checkbook could not be moved
	// aside. Nothing happened: the checkbook is where it was, with its
	// write-ahead log beside it, and the restored file is untouched.
	ErrCheckbookNotMoved = cerrs.Error("the checkbook could not be moved aside")

	// ErrNotPutInPlace is returned when the checkbook was moved aside and the
	// restored file could not take its name. The checkbook has been moved back,
	// so this is a restore that did not happen rather than a checkbook that is
	// gone.
	ErrNotPutInPlace = cerrs.Error("the restored checkbook could not be put in place")

	// ErrCheckbookDisplaced is the one unacceptable outcome, and it earns a
	// sentinel of its own so a caller can answer it differently from every
	// other failure: the checkbook was moved aside, the restored file could not
	// take its name, and the checkbook could not be moved back. Nothing was
	// deleted -- both files are named in the error -- but there is no file at
	// the checkbook's name and only a person can decide which one belongs there.
	ErrCheckbookDisplaced = cerrs.Error("there is no file at the checkbook's name")
)

// renameAttempts and renamePause are the bounded retry around the two renames
// that matter.
//
// On Windows a file with an open handle cannot be renamed, and antivirus,
// Windows Search and OneDrive all hold a file transiently after it is closed.
// On Unix the loop never runs twice. Ten lines is a cheap price for the swap not
// failing because a scanner happened to be reading the folder.
const (
	renameAttempts = 5
	renamePause    = 100 * time.Millisecond
)

// Replace puts the restored checkbook at the checkbook's name and keeps what was
// there, returning the name it was kept under.
//
// The caller must have closed the checkbook first. On Windows the rename failing
// is the assertion; on Unix there is nothing to assert, which is why this is
// documented rather than checked -- the same way Restore documents that it never
// writes over its destination.
//
// An absent checkbook is not an error. That is the case where the household's
// -db never opened at all, which is one of the main reasons to restore: kept
// comes back empty and the restored file simply takes the name.
//
// Nothing is deleted. The checkbook that was there is moved, not removed, and it
// keeps its write-ahead log, so it can be opened again by pointing at its new
// name. That is also how BK-1 is satisfied here: a reviewer will reach for "take
// a backup before a risky operation", and the move-aside *is* that copy -- it is
// timestamped, complete, and, carrying AppID rather than BackupAppID, openable
// directly, which is a shorter road back than a file that must itself be
// restored. Taking a backup first was considered and rejected: VACUUM INTO fails
// on a damaged database, so requiring one would block exactly the emergency this
// exists for.
//
// There is no context. Three renames are not cancellable, and a context accepted
// and ignored is a lie.
func Replace(restored, checkbook string) (kept string, err error) {
	if restored == "" {
		return "", ErrMissingSource
	}
	if checkbook == "" {
		return "", ErrMissingDestination
	}
	if sameFile(restored, checkbook) {
		return "", fmt.Errorf("%s: %w", restored, ErrSamePath)
	}

	// Asked what it is before anything moves. A backup would pass every rename
	// below and then refuse to open, and by then the household's checkbook is
	// already somewhere else.
	appID, err := storage.ApplicationID(restored)
	if err != nil {
		return "", fmt.Errorf("%s: %w: %w", restored, ErrNotCheckbook, err)
	}
	if appID != storage.AppID {
		if appID == storage.BackupAppID {
			return "", fmt.Errorf("%s: %w: it is a backup, and a backup is restored rather than put in place", restored, ErrNotCheckbook)
		}
		return "", fmt.Errorf("%s: %w: application_id is %d", restored, ErrNotCheckbook, appID)
	}

	// Restore leaves no log beside what it wrote, and asserts as much before
	// naming it. One here means something else has the file open, or that an
	// earlier operation did not finish -- and a database put in place without
	// the log holding its most recent records is the quiet loss this whole
	// sequence is arranged to avoid.
	if !free(restored + "-wal") {
		return "", fmt.Errorf("%w: %s was left beside it, so it is not one self-contained file",
			ErrNotVerified, filepath.Base(restored+"-wal"))
	}

	dir := filepath.Dir(checkbook)

	// Nothing at the checkbook's name: no move-aside to make, and no window to
	// open. Straight to the one rename.
	if free(checkbook) {
		if err := renameRetry(restored, checkbook); err != nil {
			return "", fmt.Errorf("%w: %w", ErrNotPutInPlace, err)
		}
		return "", nil
	}

	// The same stamp as the restored file, so a folder interrupted mid-swap is
	// legible to somebody reading it: the two names say they belong together.
	stamp, ok := StampInName(restored)
	if !ok {
		stamp = time.Now()
	}
	aside, err := freeStamped(dir, replacedPrefix, stamp)
	if err != nil {
		return "", err
	}

	// The sidecars move with the database, and they move back in reverse.
	//
	// SQLite binds a write-ahead log to its database by filename and nothing
	// inside the log records a path, so a .db and its -wal renamed together
	// recover exactly as they would have in place. The bug this ordering avoids
	// is the -wal moving and the .db failing to: the checkbook would still be at
	// its name while its committed frames sat beside another one, and reopening
	// it would silently lose every transaction in that log. That is the quiet
	// loss CO-3 forbids, arriving through a filesystem instead of an UPDATE.
	//
	// A -shm holds no records and may be removed if it will not move. A -wal
	// never may.
	shmMoved, err := moveSidecar(checkbook+"-shm", aside+"-shm", true)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrCheckbookNotMoved, err)
	}
	walMoved, err := moveSidecar(checkbook+"-wal", aside+"-wal", false)
	if err != nil {
		undoSidecar(shmMoved, aside+"-shm", checkbook+"-shm")
		return "", fmt.Errorf("%w: %w", ErrCheckbookNotMoved, err)
	}

	// The window opens here: from this rename until the next one there is no
	// file at the checkbook's name. The caller holds its control lock across
	// both, so nothing else can open -- and therefore create -- a checkbook at
	// that name in between.
	if err := renameRetry(checkbook, aside); err != nil {
		undoSidecar(walMoved, aside+"-wal", checkbook+"-wal")
		undoSidecar(shmMoved, aside+"-shm", checkbook+"-shm")
		return "", fmt.Errorf("%w: %w", ErrCheckbookNotMoved, err)
	}

	// Not paranoia: os.Rename replaces an existing file on both platforms, so
	// this is what catches a second swap racing this one rather than quietly
	// writing over whatever it just put there.
	if !free(checkbook) {
		if putBack(aside, checkbook, walMoved, shmMoved) == nil {
			return "", fmt.Errorf("%s: %w: another file appeared at that name", checkbook, ErrNotPutInPlace)
		}
		return "", fmt.Errorf("%w: your records are in %s, and %s is what appeared at the checkbook's name",
			ErrCheckbookDisplaced, aside, checkbook)
	}

	if err := renameRetry(restored, checkbook); err != nil {
		// Compensation is a strict mirror of the forward path. That symmetry is
		// what makes it reviewable.
		if backErr := putBack(aside, checkbook, walMoved, shmMoved); backErr != nil {
			return "", fmt.Errorf("%w: your records are in %s and the restored copy is in %s; move one of them to %s by hand: %w",
				ErrCheckbookDisplaced, aside, restored, checkbook, backErr)
		}
		return "", fmt.Errorf("%w: %w", ErrNotPutInPlace, err)
	}
	return aside, nil
}

// moveSidecar renames one of SQLite's companion files, if it is there.
//
// removable says whether losing it is acceptable. A -shm is a shared-memory
// index that SQLite rebuilds, so it may be removed when it will not move; a -wal
// holds committed records and may not.
func moveSidecar(from, to string, removable bool) (moved bool, err error) {
	if free(from) {
		return false, nil
	}
	if err := renameRetry(from, to); err == nil {
		return true, nil
	} else if !removable {
		return false, fmt.Errorf("move %s to %s: %w", filepath.Base(from), filepath.Base(to), err)
	}
	if err := os.Remove(from); err != nil {
		return false, fmt.Errorf("remove %s: %w", filepath.Base(from), err)
	}
	return false, nil
}

// undoSidecar puts one back, if it was moved. A failure here is not reported:
// the caller is already returning a failure that says the checkbook was not
// moved, and that remains true -- the database itself never left its name.
func undoSidecar(moved bool, from, to string) {
	if moved {
		_ = renameRetry(from, to)
	}
}

// putBack is the compensation for a swap that could not be completed: the
// database first, then its log, then its index, which is the forward order
// reversed.
func putBack(aside, checkbook string, walMoved, shmMoved bool) error {
	if err := renameRetry(aside, checkbook); err != nil {
		return fmt.Errorf("move %s back to %s: %w", aside, checkbook, err)
	}
	if walMoved {
		if err := renameRetry(aside+"-wal", checkbook+"-wal"); err != nil {
			return fmt.Errorf("move %s back: %w", filepath.Base(aside+"-wal"), err)
		}
	}
	if shmMoved {
		// A -shm that will not come back is not worth failing over: SQLite
		// rebuilds it from the log.
		_ = renameRetry(aside+"-shm", checkbook+"-shm")
	}
	return nil
}

// renameFile is os.Rename, indirected so that the compensation paths below can
// be pinned by a test.
//
// Nothing in a filesystem lets a test make one rename in a folder fail while its
// neighbours succeed, and the two paths that need exactly that -- a -wal that
// moved while its database did not, and a swap that could neither finish nor be
// undone -- are the two whose failure is silent. A package variable is a small
// price for having them under test rather than under argument. It is never
// reassigned outside a test.
var renameFile = os.Rename

// renameRetry is os.Rename with the bounded retry renameAttempts describes.
func renameRetry(from, to string) error {
	var err error
	for attempt := 1; ; attempt++ {
		if err = renameFile(from, to); err == nil {
			return nil
		}
		if attempt == renameAttempts {
			return err
		}
		time.Sleep(renamePause)
	}
}

// sameFile reports whether two paths name one file.
//
// os.SameFile where both exist, which sees through a symlink and through two
// spellings of one path; a cleaned string comparison otherwise, since a file
// that is not there cannot be stated.
func sameFile(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr == nil && berr == nil {
		return os.SameFile(ai, bi)
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(absA) == filepath.Clean(absB)
}
