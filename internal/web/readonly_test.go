// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/storage"
)

// openBackup gives a checkbook opened the way a backup is looked at: read-only,
// on a file nothing else has open.
func openBackup(t *testing.T) *storage.Store {
	t.Helper()

	store, path := openFile(t)
	seed(t, store)
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ro, err := storage.OpenReadOnly(t.Context(), path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return ro
}

// TestReadOnlyWithholdsEveryWriteAction. The rule is withheld rather than
// offered and then refused: a button that can only fail is worse than no button.
func TestReadOnlyWithholdsEveryWriteAction(t *testing.T) {
	h := server(t, openBackup(t))

	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, `class="readonly"`) {
		t.Error("the frame does not mark the checkbook read-only")
	}
	if !strings.Contains(body, "nothing can be changed") {
		t.Error("the frame does not say what read-only means")
	}
	for _, unwanted := range []string{
		"Add a transaction", // the entry form
		"Mark cleared",      // the status column's control
		"+ Add account",
		"Back up now",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("a read-only register still offers %q", unwanted)
		}
	}
	// The register itself is still there. Read-only is for reading.
	for _, want := range []string{"Riba Smith", "Ending balance", "Close checkbook"} {
		if !strings.Contains(body, want) {
			t.Errorf("a read-only register does not show %q", want)
		}
	}
}

// TestReadOnlyRefusesAWriteThatArrivesAnyway: an old tab, a bookmarked form, or
// a typed address. The answer is a written explanation, not SQLite's "attempt to
// write a readonly database" (RG-4).
func TestReadOnlyRefusesAWriteThatArrivesAnyway(t *testing.T) {
	h := server(t, openBackup(t))

	for _, tt := range []struct {
		name   string
		method string
		path   string
		form   url.Values
	}{
		// The form page is a GET, because it is an offer; the rest are the
		// POSTs a form left open in another tab would send.
		{"the new-account form", http.MethodGet, "/accounts/new", nil},
		{"creating an account", http.MethodPost, "/accounts", url.Values{"name": {"Savings"}, "type": {"savings"}, "currency": {"USD"}}},
		{"entering a transaction", http.MethodPost, "/accounts/1/transactions", entryValues()},
		{"marking a row cleared", http.MethodPost, "/accounts/1/transactions/1/status", url.Values{"status": {"cleared"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			send := func() *httptest.ResponseRecorder { return postFromPage(t, h, tt.path, tt.form) }
			if tt.method == http.MethodGet {
				send = func() *httptest.ResponseRecorder { return get(t, h, tt.path) }
			}

			w := send()
			body := w.Body.String()

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409", w.Code)
			}
			if !strings.Contains(body, "open for reading only") {
				t.Error("the refusal does not say why")
			}
			if !strings.Contains(body, "What to do next") {
				t.Error("the refusal does not say what to do next (RG-4)")
			}
			if strings.Contains(body, "readonly database") {
				t.Error("SQLite's own words reached the reader")
			}
		})
	}
}

// TestReadOnlyIsOfferedOnTheOpenForm. The box is what the reader ticks to look
// at a backup, so it has to be on the form and has to say what it does.
func TestReadOnlyIsOfferedOnTheOpenForm(t *testing.T) {
	store := open(t)
	h := newServer(t, webOptionsWithOpener(t, store))

	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	body := get(t, h, "/checkbook").Body.String()
	if !strings.Contains(body, `name="readonly"`) {
		t.Error("the open form does not offer to open a backup read-only")
	}
	if !strings.Contains(body, "nothing is written to the file") {
		t.Error("the read-only box does not say what it does")
	}
	if !strings.Contains(body, "A backup opens read-only and no other") {
		t.Error("the open form does not say a backup opens only read-only")
	}
}
