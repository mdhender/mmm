// Copyright (c) 2026 Michael D Henderson.

package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mdhender/mmm/internal/storage"
)

// FolderName is the directory Back up now writes into, beside the checkbook.
//
// It is the one place the convention is spelled. backup.Create learns none of
// it: dir stays an argument, so a household that wants their copies somewhere
// else still gets them there, and the TUI that does not exist yet gets the same
// convention by calling the same function rather than by remembering a string.
const FolderName = "backups"

// maxListed is how many files Find will open the header of.
//
// Reading an application id opens a connection per file, so a folder that has
// accumulated years of backups would otherwise turn a page into a few hundred
// database opens. The files are stated and sorted first, so the fifty that are
// confirmed are the fifty most recent -- which are the ones anybody restoring is
// choosing between.
const maxListed = 50

// Backup is one file that could be restored from.
type Backup struct {
	// Path is the file, absolute.
	Path string

	// Taken is the file's modification time. It is what the list is ordered by.
	// The stamp shown to the reader comes from the name when the name is one
	// this program wrote -- see StampInName -- because that is the moment the
	// copy was of, and a file that has been moved between disks carries the
	// name and not the time.
	Taken time.Time

	// Bytes is its size.
	Bytes int64

	// IsBackup distinguishes a backup from a checkbook that could still be
	// restored from. Both are offered: copying a checkbook to a second machine
	// is a reasonable thing to want, and Restore accepts either.
	IsBackup bool
}

// Folder is where Back up now writes, given the checkbook it is copying.
func Folder(checkbook string) string {
	return filepath.Join(filepath.Dir(checkbook), FolderName)
}

// Find lists the files in dir and dir/backups that this program could restore
// from, newest first.
//
// Both places are read because backups written before there was a folder are
// still backups and are not moved. Nothing here is created: a missing
// dir/backups is simply a folder with nothing in it yet, which is the ordinary
// state of a checkbook that has never been backed up.
//
// A file is a backup because its header says so (BK-6, last sentence). The
// folder is where we look; storage.ApplicationID is what decides. Nothing here
// infers from a name or a path, so a backup renamed notes.txt is found and a
// text file named checkbook-20260101-000000.db is not.
func Find(dir string) ([]Backup, error) {
	if dir == "" {
		return nil, ErrMissingSource
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s: %w", dir, ErrMissingDirectory)
	}

	// Cleaned absolute paths, because dir/backups may be a symlink to dir and a
	// household should not be offered the same file twice under two names.
	seen := make(map[string]bool)
	var found []Backup

	for _, where := range []string{dir, filepath.Join(dir, FolderName)} {
		entries, err := os.ReadDir(where)
		if err != nil {
			if where == dir {
				return nil, fmt.Errorf("read %s: %w", where, err)
			}
			// The backups folder is not there, or cannot be read. Neither is a
			// failure of the listing: the files beside the checkbook are still
			// worth showing, and a folder that does not exist yet is the
			// ordinary state before the first backup.
			continue
		}
		for _, entry := range entries {
			// Skipped by name, before anything is opened. A concurrent restore's
			// working copy is a half-written database and should not be opened
			// at all, and a -wal is not a file anybody restores from.
			if entry.IsDir() || skipByName(entry.Name()) {
				continue
			}
			path := filepath.Join(where, entry.Name())
			abs, err := filepath.Abs(path)
			if err != nil {
				continue
			}
			abs = filepath.Clean(abs)
			if seen[abs] {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.IsDir() || info.Size() == 0 {
				continue
			}
			seen[abs] = true
			found = append(found, Backup{Path: abs, Taken: info.ModTime(), Bytes: info.Size()})
		}
	}

	// Sorted before the headers are read, so the fifty that get opened are the
	// fifty most recent rather than whichever fifty the directory happened to
	// list first.
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Taken.Equal(found[j].Taken) {
			return found[i].Path < found[j].Path
		}
		return found[i].Taken.After(found[j].Taken)
	})
	if len(found) > maxListed {
		found = found[:maxListed]
	}

	var ours []Backup
	for _, b := range found {
		appID, err := storage.ApplicationID(b.Path)
		if err != nil {
			continue
		}
		switch appID {
		case storage.BackupAppID:
			b.IsBackup = true
		case storage.AppID:
			b.IsBackup = false
		default:
			continue
		}
		ours = append(ours, b)
	}
	return ours, nil
}

// skipByName reports whether a directory entry is one Find must not open.
//
// SQLite's sidecars hold no records anybody restores from, and .checkbook-*.tmp
// is a working copy that another operation may be writing at this moment.
func skipByName(name string) bool {
	switch {
	case strings.HasSuffix(name, "-wal"), strings.HasSuffix(name, "-shm"), strings.HasSuffix(name, "-journal"):
		return true
	case strings.HasPrefix(name, ".checkbook-") && strings.HasSuffix(name, ".tmp"):
		return true
	}
	return false
}

// StampInName reports the moment recorded in a name this program wrote.
//
// A backup's name carries the local calendar clock of the moment it was taken
// (see nameLayout), and that is a better answer than a modification time: a
// backup copied to another disk keeps its name and loses its time. The second
// return value is false for a name this program did not write, and the caller
// then has nothing but the file's own time -- which is why a page showing these
// has to say which of the two it is showing.
func StampInName(path string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(filepath.Base(path), "checkbook-")
	if !ok {
		return time.Time{}, false
	}
	rest, ok = strings.CutSuffix(rest, ".db")
	if !ok {
		return time.Time{}, false
	}
	// checkbook-replaced-20260902-153104.db is one of ours too: it is what a
	// restore left behind, and it is restorable from.
	rest = strings.TrimPrefix(rest, "replaced-")
	rest = strings.TrimPrefix(rest, "restored-")

	// The counter a second copy inside one second carries is not part of the
	// stamp: "20260902-141530-2" is the same second as "20260902-141530".
	if parts := strings.Split(rest, "-"); len(parts) == 3 {
		rest = parts[0] + "-" + parts[1]
	}

	// Local, because that is what the name was written in. ParseInLocation
	// rather than Parse, which would read it as UTC and shift the displayed
	// time by the household's offset.
	t, err := time.ParseInLocation(nameLayout, rest, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
