// Copyright (c) 2026 Michael D Henderson.

package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The three names this program gives a file it wrote. Each is a prefix followed
// by nameLayout's stamp and ".db", with "-2", "-3" and so on appended when two
// land inside one second.
//
//	checkbook-20260902-141530.db           a backup
//	checkbook-restored-20260902-153104.db  a backup brought back as a checkbook
//	checkbook-replaced-20260902-153104.db  the checkbook a restore displaced
//
// The restore pair share a stamp deliberately: a folder somebody is reading
// after an interrupted swap should say which two files belong together.
const (
	backupPrefix   = "checkbook-"
	restoredPrefix = "checkbook-restored-"
	replacedPrefix = "checkbook-replaced-"
)

// RestoredName picks a free name for the checkbook a restore is about to write.
//
// It is exported because the browser has to have both names in hand before the
// long step begins: a restore that copies a database and then finds it has
// nowhere to put it has spent the household's time for nothing.
func RestoredName(dir string, now time.Time) (string, error) {
	return freeStamped(dir, restoredPrefix, now)
}

// ValidBackupName reports whether name is one Create wrote.
//
// The name arrives back through a URL, which anything can compose, and it goes
// into a sentence the reader is meant to believe. Templates escape it either
// way; this is about not saying something untrue.
func ValidBackupName(name string) bool {
	// checkbook-restored- and checkbook-replaced- both begin with the backup
	// prefix, and neither is a backup.
	if strings.HasPrefix(name, restoredPrefix) || strings.HasPrefix(name, replacedPrefix) {
		return false
	}
	return validStampedName(name, backupPrefix)
}

// ValidReplacedName reports whether name is one Replace wrote: the checkbook
// that a restore moved aside. Same rule and same reason as ValidBackupName.
func ValidReplacedName(name string) bool { return validStampedName(name, replacedPrefix) }

// validStampedName checks the shape without consulting the filesystem.
func validStampedName(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".db")
	if !ok {
		return false
	}
	// 20260902-141530, and 20260902-141530-2 for the second to land in the same
	// second.
	parts := strings.Split(rest, "-")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	if len(parts[0]) != 8 || !allDigits(parts[0]) {
		return false
	}
	if len(parts[1]) != 6 || !allDigits(parts[1]) {
		return false
	}
	return len(parts) == 2 || allDigits(parts[2])
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// freeStamped picks a name under prefix that nothing is using.
//
// Two files inside one second are unlikely and not impossible, so the name is
// checked and a counter added rather than an earlier one being written over.
// Losing a backup to a backup would be the worst possible way to lose one.
func freeStamped(dir, prefix string, now time.Time) (string, error) {
	stamp := now.Format(nameLayout)
	for n := 1; n <= 100; n++ {
		name := prefix + stamp + ".db"
		if n > 1 {
			name = prefix + stamp + "-" + strconv.Itoa(n) + ".db"
		}
		path := filepath.Join(dir, name)
		// The sidecars count too. A -wal at the name we are about to take would
		// be adopted by the file that takes it.
		if free(path) && free(path+"-wal") && free(path+"-shm") {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: %w", filepath.Join(dir, prefix+stamp+".db"), ErrNameInUse)
}

// free reports whether nothing at all is at path.
func free(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
