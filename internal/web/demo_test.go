// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/storage"
)

// TestTemporaryDatabaseIsMarkedOnEveryPage: a register that keeps nothing looks
// exactly like one that does, and somebody entering real transactions into the
// demo would lose them without ever being told. The mark is in the frame, so it
// is on every page rather than on the ones somebody remembered.
func TestTemporaryDatabaseIsMarkedOnEveryPage(t *testing.T) {
	store := open(t)
	h := server(t, store)

	// Before any account exists, and after: the empty page, the form, a
	// register, and an error page all come through the same frame.
	for _, tt := range []struct {
		name string
		path string
		seed bool
	}{
		{"the empty database", "/", false},
		{"the new-account form", "/accounts/new", false},
		{"a register", "/accounts/1", true},
		{"an error page", "/accounts/99", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.seed && len(accounts(t, store)) == 0 {
				seed(t, store)
			}

			body := get(t, h, tt.path).Body.String()
			if !strings.Contains(body, `class="ephemeral"`) {
				t.Error("the page does not mark the database as a temporary one")
			}
			if !strings.Contains(body, "nothing is saved") {
				t.Error("the page does not say that nothing is saved")
			}
			// Twice: once at the top, where somebody about to type is looking,
			// and once beside the path at the bottom.
			if got := strings.Count(body, "&#8987;"); got != 2 {
				t.Errorf("the impermanence mark appears %d times, want 2 (the top bar and the status bar)", got)
			}
		})
	}
}

// TestFileBackedDatabaseIsNotMarked is the other half: the mark means something
// only if the register holding the household's records does not carry it.
func TestFileBackedDatabaseIsNotMarked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkbook.db")
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if store.IsMemory() {
		t.Error("a file-backed store reports itself as held in memory")
	}

	body := get(t, server(t, store), "/").Body.String()
	for _, unwanted := range []string{"ephemeral", "nothing is saved", "&#8987;"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("a register on %s is marked temporary: found %q", path, unwanted)
		}
	}
	if !strings.Contains(body, path) {
		t.Error("the page does not name the database file in use (BK-3)")
	}
}
