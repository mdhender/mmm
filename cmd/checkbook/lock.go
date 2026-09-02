// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/web"
)

// lockSuffix is appended to the database path to name its lock file. It sits
// beside the database because the database, not the port, is the thing two
// copies of the program must not share: with the default -port of 0 they would
// each be given a free port and neither would notice the other.
const lockSuffix = ".lock"

// probeTimeout bounds the check for a running instance. The address is on this
// machine, so anything slower than this is not answering.
const probeTimeout = 2 * time.Second

// lockFileHeader is written at the top of every lock file, for whoever finds one
// and wonders what it is.
const lockFileHeader = `# This file marks a running copy of the checkbook program.
#
# It is removed when the program stops. If the program was killed or crashed it
# may be left behind, which is harmless: the next start checks whether the
# address below still answers, and takes the file over if it does not.
#
# It is safe to delete while the program is not running.
`

// instance describes a copy of the program that holds a database.
type instance struct {
	URL      string
	PID      int
	Started  string
	Database string
}

// lock is a held claim on a database, released by Release.
type lock struct {
	path string
}

// Release removes the lock file. It is safe to call more than once.
func (l *lock) Release() {
	if l == nil || l.path == "" {
		return
	}
	// Only ever the file this process created. A failure here is not worth
	// reporting: the next start treats an orphaned lock as stale anyway.
	_ = os.Remove(l.path)
	l.path = ""
}

// acquireLock claims database for this process, publishing url as the address
// the register is being served from.
//
// It returns a lock on success. If another copy of the program is already
// serving this database it returns that instance instead, with a nil lock, and
// nothing is changed.
//
// A lock file left behind by a crash is not a problem to be reported to the
// user: the address in it is probed, and a file whose address does not answer as
// this same database is taken over. There is no ritual for the household to
// perform and no stale-lock message to decipher.
func acquireLock(database, url string, pid int) (*lock, *instance, error) {
	path := database + lockSuffix

	// Two attempts: create, and if the file already exists but turns out to be
	// stale, remove it and create again. A third would mean another process is
	// racing us for the same database, and it has won.
	for attempt := range 2 {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			werr := writeLockFile(f, instance{
				URL:      url,
				PID:      pid,
				Started:  storage.FormatTime(time.Now()),
				Database: database,
			})
			cerr := f.Close()
			if werr == nil {
				werr = cerr
			}
			if werr != nil {
				_ = os.Remove(path)
				return nil, nil, fmt.Errorf("write %s: %w", path, werr)
			}
			return &lock{path: path}, nil, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("create %s: %w", path, err)
		}

		held, err := readLockFile(path)
		if err != nil {
			// Unreadable or truncated: a crash mid-write. Treat it as stale.
			held = instance{}
		}
		if running(held, database) {
			return nil, &held, nil
		}
		if attempt == 0 {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, nil, fmt.Errorf("remove stale %s: %w", path, err)
			}
		}
	}

	// Lost the race. Whoever holds it now is the running instance.
	held, err := readLockFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return nil, &held, nil
}

// running reports whether the recorded instance is still serving database.
//
// The check is an HTTP request rather than a look at the process id, because
// asking whether a process id is alive means different things on each platform
// and answers the wrong question anyway: what matters is whether the register is
// actually being served. The database is compared as well, so a lock left behind
// by a crash cannot be matched by an unrelated instance that happened to be
// given the same port afterwards.
func running(held instance, database string) bool {
	if held.URL == "" {
		return false
	}

	client := &http.Client{
		Timeout: probeTimeout,
		// The register redirects; the header is on every response, so there is
		// no reason to follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodHead, held.URL, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.Header.Get(web.DatabaseHeader) == database
}

// writeLockFile records an instance.
func writeLockFile(f *os.File, in instance) error {
	_, err := fmt.Fprintf(f, "%surl = %s\npid = %d\nstarted = %s\ndatabase = %s\n",
		lockFileHeader, in.URL, in.PID, in.Started, in.Database)
	return err
}

// readLockFile parses a lock file. Unknown keys are ignored, so a file written
// by a later version is read as far as it can be rather than rejected.
func readLockFile(path string) (instance, error) {
	f, err := os.Open(path)
	if err != nil {
		return instance{}, err
	}
	defer f.Close()

	var in instance
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "url":
			in.URL = value
		case "pid":
			_, _ = fmt.Sscanf(value, "%d", &in.PID)
		case "started":
			in.Started = value
		case "database":
			in.Database = value
		}
	}
	return in, scanner.Err()
}

// databasePath resolves the -db value the way every copy of the program will, so
// that two of them started from different directories agree on whether they are
// looking at the same file.
func databasePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", path, err)
	}
	// Best effort: the file may not exist yet, in which case the absolute path
	// is the most agreement available.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// alreadyRunning reports that this database is already open, and brings the copy
// that has it to the front.
//
// Starting the program twice is usually a mistake rather than an intention --
// the second click on an icon, or a forgotten window behind everything else --
// so the answer is to show the register that already exists rather than to
// refuse and leave the household with nothing. The exit status is still a
// failure, because what was asked for did not happen: no second copy was
// started.
func alreadyRunning(out io.Writer, other instance, database string, openBrowser bool, log logger) error {
	fmt.Fprintf(out, "checkbook: this checkbook is already open\n")
	fmt.Fprintf(out, "  database: %s\n", database)
	fmt.Fprintf(out, "  register: %s\n", other.URL)
	if other.PID != 0 {
		fmt.Fprintf(out, "  started:  %s by process %d\n", other.Started, other.PID)
	}
	fmt.Fprintf(out, "\nOnly one copy of the program may have a checkbook open at a time, so\n")
	fmt.Fprintf(out, "that two registers cannot save over each other. Use the one above; it\n")
	fmt.Fprintf(out, "has the same records. To open a different checkbook, pass -db.\n")

	if openBrowser {
		if err := openInBrowser(other.URL); err != nil {
			log.Warn("could not open a browser", "url", other.URL, "err", err)
		}
	}
	return errReported
}

// logger is the little of slog.Logger this file needs, so the message above can
// be tested without one.
type logger interface {
	Warn(msg string, args ...any)
}
