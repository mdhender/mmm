// Copyright (c) 2026 Michael D Henderson.

package category_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/storage"
)

func open(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.OpenMemory(t.Context(), strings.ReplaceAll(t.Name(), "/", "-"))
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = s.Close() }) })
	return s
}

func TestEnsureCreatesOnce(t *testing.T) {
	store := open(t)

	first, err := category.Ensure(t.Context(), store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	second, err := category.Ensure(t.Context(), store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("Ensure created a second category: %d then %d", first.ID, second.ID)
	}
}

// TestEnsureIsCaseInsensitive matches the schema's COLLATE NOCASE. Two spellings
// of one category would quietly split a year of spending in half.
func TestEnsureIsCaseInsensitive(t *testing.T) {
	store := open(t)

	first, err := category.Ensure(t.Context(), store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	second, err := category.Ensure(t.Context(), store, "groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("%q and %q became different categories", "Groceries", "groceries")
	}
	// The spelling entered first is the one kept.
	if second.Name != "Groceries" {
		t.Errorf("stored name = %q, want %q", second.Name, "Groceries")
	}
}

func TestEnsureTrimsAndRejectsBlank(t *testing.T) {
	store := open(t)

	got, err := category.Ensure(t.Context(), store, "  Utilities  ")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got.Name != "Utilities" {
		t.Errorf("stored name = %q, want %q", got.Name, "Utilities")
	}

	if _, err := category.Ensure(t.Context(), store, "   "); !errors.Is(err, category.ErrMissingName) {
		t.Fatalf("Ensure(blank) = %v, want ErrMissingName", err)
	}
}

func TestListIsOrderedByName(t *testing.T) {
	store := open(t)

	for _, name := range []string{"Utilities", "Dining", "Groceries"} {
		if _, err := category.Ensure(t.Context(), store, name); err != nil {
			t.Fatalf("Ensure(%q): %v", name, err)
		}
	}

	got, err := category.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"Dining", "Groceries", "Utilities"}
	if len(got) != len(want) {
		t.Fatalf("List returned %d categories, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("List[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}
