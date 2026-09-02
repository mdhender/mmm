// Copyright (c) 2026 Michael D Henderson.

package backup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/storage"
)

// This file is inside the package for one reason: the compensation paths in
// Replace are the ones whose failure is silent, and no filesystem lets a test
// make one rename in a folder fail while its neighbours succeed. renameFile is
// swapped for a stub that fails exactly the move under test, which is what turns
// "the -wal comes back" from an argument in a comment into an assertion.

// failRenames replaces renameFile with one that refuses any move whose source
// base name is in the list, and restores it when the test ends.
func failRenames(t *testing.T, bases ...string) {
	t.Helper()

	blocked := make(map[string]bool, len(bases))
	for _, base := range bases {
		blocked[base] = true
	}
	previous := renameFile
	renameFile = func(from, to string) error {
		if blocked[filepath.Base(from)] {
			return errors.New("device or resource busy")
		}
		return previous(from, to)
	}
	t.Cleanup(func() { renameFile = previous })
}

// swapReady builds the folder a swap runs in: a checkbook with a -wal and a -shm
// beside it, and a file that is a genuine checkbook waiting to take its name.
func swapReady(t *testing.T) (restored, checkbook string) {
	t.Helper()

	dir := t.TempDir()
	checkbook = filepath.Join(dir, "checkbook.db")

	store, err := storage.Open(t.Context(), checkbook)
	if err != nil {
		t.Fatalf("open the checkbook: %v", err)
	}
	// Closed at once: Replace's precondition is that the checkbook is not open,
	// and what it needs from this file is a header carrying AppID.
	if err := store.Close(); err != nil {
		t.Fatalf("close the checkbook: %v", err)
	}

	// The restored copy is a plain copy of the checkbook, which is what one is:
	// a file carrying AppID that is not yet at the checkbook's name.
	body, err := os.ReadFile(checkbook)
	if err != nil {
		t.Fatalf("read the checkbook: %v", err)
	}
	restored = filepath.Join(dir, "checkbook-restored-20260902-153104.db")
	if err := os.WriteFile(restored, body, 0o644); err != nil {
		t.Fatalf("write the restored copy: %v", err)
	}

	if err := os.WriteFile(checkbook+"-wal", []byte("committed frames"), 0o644); err != nil {
		t.Fatalf("write the -wal: %v", err)
	}
	if err := os.WriteFile(checkbook+"-shm", []byte("index"), 0o644); err != nil {
		t.Fatalf("write the -shm: %v", err)
	}
	return restored, checkbook
}

// TestReplacePutsTheWalBackWhenTheAsideCannotFinish is the silent-loss bug.
//
// The -wal moves first and the database second. If the database cannot follow,
// the log must come back: leaving it beside another name would mean reopening
// the checkbook and losing every transaction the log still held -- the quiet
// loss CO-3 forbids, arriving through a filesystem instead of an UPDATE.
func TestReplacePutsTheWalBackWhenTheAsideCannotFinish(t *testing.T) {
	restored, checkbook := swapReady(t)
	failRenames(t, "checkbook.db")

	kept, err := Replace(restored, checkbook)
	if err == nil {
		t.Fatal("Replace reported success though the checkbook could not be moved aside")
	}
	if !errors.Is(err, ErrCheckbookNotMoved) {
		t.Errorf("err = %v, want %v", err, ErrCheckbookNotMoved)
	}
	if kept != "" {
		t.Errorf("Replace named a kept file, %q, for a move that did not happen", kept)
	}

	if got, err := os.ReadFile(checkbook + "-wal"); err != nil || string(got) != "committed frames" {
		t.Errorf("the -wal is not beside its database: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(checkbook + "-shm"); err != nil || string(got) != "index" {
		t.Errorf("the -shm is not beside its database: %q (%v)", got, err)
	}
	if _, err := os.Stat(checkbook); err != nil {
		t.Errorf("the checkbook is not at its own name: %v", err)
	}
	// And nothing was left at the name the swap was going to use.
	assertNoStrays(t, filepath.Dir(checkbook), replacedPrefix)
}

// TestReplacePutsTheOriginalBackWhenItCannotFinish. The checkbook moved aside and
// the restored copy could not take its name, so the checkbook goes back to it:
// this is a restore that did not happen, not a checkbook that is gone.
func TestReplacePutsTheOriginalBackWhenItCannotFinish(t *testing.T) {
	restored, checkbook := swapReady(t)
	failRenames(t, filepath.Base(restored))

	kept, err := Replace(restored, checkbook)
	if err == nil {
		t.Fatal("Replace reported success though the restored copy could not be put in place")
	}
	if !errors.Is(err, ErrNotPutInPlace) {
		t.Errorf("err = %v, want %v", err, ErrNotPutInPlace)
	}
	if kept != "" {
		t.Errorf("Replace named a kept file, %q, for a swap that did not happen", kept)
	}

	// Everything is back where it started, log and index included.
	if _, err := os.Stat(checkbook); err != nil {
		t.Errorf("the checkbook is not at its own name: %v", err)
	}
	if got, err := os.ReadFile(checkbook + "-wal"); err != nil || string(got) != "committed frames" {
		t.Errorf("the -wal did not come back: %q (%v)", got, err)
	}
	if _, err := os.Stat(restored); err != nil {
		t.Errorf("the restored copy is gone: %v", err)
	}
	assertNoStrays(t, filepath.Dir(checkbook), replacedPrefix)
}

// TestReplaceSaysSoWhenNothingIsAtTheCheckbooksName is the one unacceptable
// outcome: the checkbook moved aside, the restored copy could not take its name,
// and the checkbook could not come back. Nothing was deleted, and the message
// has to name both files, because only a person can decide which belongs where.
func TestReplaceSaysSoWhenNothingIsAtTheCheckbooksName(t *testing.T) {
	restored, checkbook := swapReady(t)
	// The restored copy cannot be put in place, and the checkbook cannot be put
	// back: both renames into the checkbook's own name fail.
	failRenames(t, filepath.Base(restored), "checkbook-replaced-20260902-153104.db")

	kept, err := Replace(restored, checkbook)
	if err == nil {
		t.Fatal("Replace reported success though nothing is at the checkbook's name")
	}
	if !errors.Is(err, ErrCheckbookDisplaced) {
		t.Fatalf("err = %v, want %v", err, ErrCheckbookDisplaced)
	}
	if kept != "" {
		t.Errorf("Replace named a kept file, %q, though the swap did not finish", kept)
	}

	aside := filepath.Join(filepath.Dir(checkbook), "checkbook-replaced-20260902-153104.db")
	for _, want := range []string{aside, restored} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message does not name %s: %v", want, err)
		}
	}

	// Nothing was deleted: both files are on disk, under names the message gave.
	for _, path := range []string{aside, aside + "-wal", restored} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is gone: %v", filepath.Base(path), err)
		}
	}
	if _, err := os.Stat(checkbook); !os.IsNotExist(err) {
		t.Errorf("something is at the checkbook's name after all: %v", err)
	}
}

// assertNoStrays checks that a compensated swap left nothing under prefix.
func assertNoStrays(t *testing.T, dir, prefix string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			t.Errorf("%s was left behind by a swap that did not happen", entry.Name())
		}
	}
}
