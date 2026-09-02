// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/web"
)

// liveInstance stands in for a running copy of the program serving database.
func liveInstance(t *testing.T, database string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(web.DatabaseHeader, database)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

func TestAcquireLockAndRelease(t *testing.T) {
	database := filepath.Join(t.TempDir(), "checkbook.db")

	held, other, err := acquireLock(database, "http://127.0.0.1:1/", 4242)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	if other != nil {
		t.Fatalf("acquireLock reported another instance on a fresh database: %+v", other)
	}
	if _, err := os.Stat(database + lockSuffix); err != nil {
		t.Fatalf("no lock file was written: %v", err)
	}

	held.Release()
	if _, err := os.Stat(database + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("lock file survived Release: %v", err)
	}
	// Stopping twice must not be an error; the deferred release can run after an
	// explicit one.
	held.Release()
}

// TestAcquireLockFindsRunningInstance is the case the whole mechanism exists
// for: a second copy of the program started on a database that is already open.
func TestAcquireLockFindsRunningInstance(t *testing.T) {
	database := filepath.Join(t.TempDir(), "checkbook.db")
	url := liveInstance(t, database)

	first, other, err := acquireLock(database, url, 4242)
	if err != nil || other != nil {
		t.Fatalf("acquireLock: %v, other = %+v", err, other)
	}
	defer first.Release()

	second, other, err := acquireLock(database, "http://127.0.0.1:2/", 4243)
	if err != nil {
		t.Fatalf("second acquireLock: %v", err)
	}
	if second != nil {
		t.Fatal("a second copy was allowed to claim a database that is already open")
	}
	if other == nil {
		t.Fatal("no running instance was reported")
	}
	if other.URL != url {
		t.Errorf("running instance URL = %q, want %q", other.URL, url)
	}
	if other.PID != 4242 {
		t.Errorf("running instance pid = %d, want 4242", other.PID)
	}
}

// TestAcquireLockTakesOverStaleLock covers a crash: the file is left behind, its
// address answers nothing, and the next start must simply proceed. A household
// should never have to find and delete a lock file.
func TestAcquireLockTakesOverStaleLock(t *testing.T) {
	database := filepath.Join(t.TempDir(), "checkbook.db")

	// Port 1 on loopback: nothing is listening, and the refusal is immediate.
	stale := "http://127.0.0.1:1/"
	if err := os.WriteFile(database+lockSuffix,
		[]byte("url = "+stale+"\npid = 999999\ndatabase = "+database+"\n"), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	held, other, err := acquireLock(database, "http://127.0.0.1:3/", 4242)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	if other != nil {
		t.Fatalf("a stale lock was mistaken for a running instance: %+v", other)
	}
	defer held.Release()

	got, err := readLockFile(database + lockSuffix)
	if err != nil {
		t.Fatalf("readLockFile: %v", err)
	}
	if got.URL != "http://127.0.0.1:3/" {
		t.Errorf("lock file url = %q, want this process's address", got.URL)
	}
}

// TestAcquireLockIgnoresAnotherDatabaseOnThatPort is the port-reuse case: an
// instance died, something else was given its port, and that something is also a
// checkbook -- but holding a different file. Matching on the address alone would
// send the reader to a stranger's register.
func TestAcquireLockIgnoresAnotherDatabaseOnThatPort(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "checkbook.db")
	elsewhere := liveInstance(t, filepath.Join(dir, "a-different-checkbook.db"))

	if err := os.WriteFile(database+lockSuffix,
		[]byte("url = "+elsewhere+"\npid = 999999\ndatabase = "+database+"\n"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	held, other, err := acquireLock(database, "http://127.0.0.1:4/", 4242)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	if other != nil {
		t.Fatalf("an instance holding a different database was treated as this one: %+v", other)
	}
	held.Release()
}

// TestAcquireLockTakesOverUnreadableLock: a crash midway through writing the
// file leaves something that cannot be parsed. It must not wedge the program.
func TestAcquireLockTakesOverUnreadableLock(t *testing.T) {
	database := filepath.Join(t.TempDir(), "checkbook.db")
	if err := os.WriteFile(database+lockSuffix, []byte("\x00\x01 garbage"), 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	held, other, err := acquireLock(database, "http://127.0.0.1:5/", 4242)
	if err != nil {
		t.Fatalf("acquireLock: %v", err)
	}
	if other != nil {
		t.Fatalf("garbage was read as a running instance: %+v", other)
	}
	held.Release()
}

func TestLockFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkbook.db"+lockSuffix)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := instance{
		URL:      "http://127.0.0.1:53219/",
		PID:      4242,
		Started:  "2026-09-01T22:45:36.000000Z",
		Database: "/Users/example/checkbook.db",
	}
	if err := writeLockFile(f, want); err != nil {
		t.Fatalf("writeLockFile: %v", err)
	}
	f.Close()

	got, err := readLockFile(path)
	if err != nil {
		t.Fatalf("readLockFile: %v", err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	// The file is meant to be readable by whoever finds it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(raw), "safe to delete") {
		t.Error("lock file does not explain itself to someone who finds it")
	}
}

// TestReadLockFileIgnoresUnknownKeys keeps a file written by a later version
// readable as far as it goes, rather than failing and looking like a crash.
func TestReadLockFileIgnoresUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x"+lockSuffix)
	if err := os.WriteFile(path, []byte(
		"# a comment\n\nurl = http://127.0.0.1:1/\nsomething-new = 7\nmalformed line\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := readLockFile(path)
	if err != nil {
		t.Fatalf("readLockFile: %v", err)
	}
	if got.URL != "http://127.0.0.1:1/" {
		t.Errorf("url = %q, want it parsed despite the unknown key", got.URL)
	}
}

// TestDatabasePathAgrees: two copies started from different directories, or with
// different spellings of the same file, have to reach the same answer or the
// lock is pointless.
func TestDatabasePathAgrees(t *testing.T) {
	dir := t.TempDir()
	direct := filepath.Join(dir, "checkbook.db")
	if err := os.WriteFile(direct, []byte{}, 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}
	indirect := filepath.Join(dir, "sub", "..", "checkbook.db")
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	a, err := databasePath(direct)
	if err != nil {
		t.Fatalf("databasePath: %v", err)
	}
	b, err := databasePath(indirect)
	if err != nil {
		t.Fatalf("databasePath: %v", err)
	}
	if a != b {
		t.Errorf("%q and %q resolved to %q and %q", direct, indirect, a, b)
	}
}

// TestAlreadyRunningMessage checks the message carries what the reader needs to
// find the copy that is already open.
func TestAlreadyRunningMessage(t *testing.T) {
	var out strings.Builder
	other := instance{URL: "http://127.0.0.1:53219/", PID: 4242, Started: "2026-09-01T22:45:36.000000Z"}

	err := alreadyRunning(&out, other, "/Users/example/checkbook.db", false, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Fatal("alreadyRunning returned no error; starting a second copy did not happen")
	}

	got := out.String()
	for _, want := range []string{
		"already open",
		"http://127.0.0.1:53219/",     // where the running one is
		"/Users/example/checkbook.db", // which database
		"4242",                        // which process
		"-db",                         // how to open a different one instead
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message does not mention %q:\n%s", want, got)
		}
	}
}
