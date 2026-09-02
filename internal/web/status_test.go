// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// hxPost posts the way htmx does, so the answer is a fragment rather than a
// redirect.
func hxPost(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// token returns one transaction's current concurrency token, as the page would
// have carried it.
func token(t *testing.T, store *storage.Store, acct account.Account, id int64) string {
	t.Helper()
	for _, e := range rows(t, store, acct) {
		if e.ID == id {
			return storage.FormatTime(e.UpdatedAt)
		}
	}
	t.Fatalf("no transaction %d in the register", id)
	return ""
}

func statusOf(t *testing.T, store *storage.Store, acct account.Account, id int64) transaction.Status {
	t.Helper()
	for _, e := range rows(t, store, acct) {
		if e.ID == id {
			return e.Status
		}
	}
	t.Fatalf("no transaction %d in the register", id)
	return ""
}

// TestMarkCleared: the uncategorized purchase in the seed is uncleared, and the
// answer is that row plus the totals, not a page.
func TestMarkCleared(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := hxPost(t, server(t, store), "/accounts/1/transactions/3/status", url.Values{
		"status":     {"cleared"},
		"updated_at": {token(t, store, acct, 3)},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	if got := statusOf(t, store, acct, 3); got != transaction.Cleared {
		t.Fatalf("stored status = %q, want cleared", got)
	}

	body := w.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<table") {
		t.Error("answered with a page; marking one row should answer with the row")
	}
	if !strings.Contains(body, `id="txn-3"`) {
		t.Error("the row is missing from the answer")
	}
	// The mark, and a control that now offers the other direction.
	if !strings.Contains(body, "Mark not cleared") {
		t.Error("the row does not offer to undo the mark")
	}
	// Marking cleared moves the cleared balance and the uncleared count, so the
	// totals come back with it rather than going stale on screen.
	if !strings.Contains(body, `hx-swap-oob="true"`) {
		t.Error("the totals were not sent back for an out-of-band swap")
	}
	// It was the only uncleared row, so the cleared balance now equals the
	// ending balance and nothing is outstanding.
	for _, want := range []string{"Cleared balance", "6,193.68", "(0 transactions)"} {
		if !strings.Contains(body, want) {
			t.Errorf("the totals are missing %q", want)
		}
	}
}

// TestMarkClearedIsAToggle: pressing it again puts the row back.
func TestMarkClearedIsAToggle(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	if w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"}, "updated_at": {token(t, store, acct, 3)},
	}); w.Code != http.StatusOK {
		t.Fatalf("first mark: %d", w.Code)
	}
	if w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"uncleared"}, "updated_at": {token(t, store, acct, 3)},
	}); w.Code != http.StatusOK {
		t.Fatalf("second mark: %d", w.Code)
	}
	if got := statusOf(t, store, acct, 3); got != transaction.Uncleared {
		t.Errorf("status = %q, want uncleared", got)
	}
}

// TestMarkClearedRefusesStaleToken is CO-3 through the whole stack: two tabs
// hold the same row, and the second is told rather than allowed to overwrite.
func TestMarkClearedRefusesStaleToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	stale := token(t, store, acct, 3) // what both tabs loaded

	if w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"}, "updated_at": {stale},
	}); w.Code != http.StatusOK {
		t.Fatalf("first tab: %d", w.Code)
	}

	w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"uncleared"}, "updated_at": {stale},
	})
	body := w.Body.String()
	if !strings.Contains(body, "changed in another window") {
		t.Errorf("the second tab was not told what happened:\n%s", body)
	}
	// The first write stands, and the stale tab is shown the truth rather than
	// silently corrected.
	if got := statusOf(t, store, acct, 3); got != transaction.Cleared {
		t.Errorf("status = %q, want cleared: the refused mark was applied anyway", got)
	}
	if !strings.Contains(body, `id="txn-3"`) {
		t.Error("the refused mark did not return the row as it now stands")
	}
}

// TestMarkClearedRefusedOnReconciled: RC-3, and the row carries no control at
// all so the refusal is the second line of defence rather than the first.
func TestMarkClearedRefusedOnReconciled(t *testing.T) {
	store := open(t)
	acct := seed(t, store) // transaction 1 is reconciled

	page := get(t, server(t, store), "/accounts/1").Body.String()
	row := page[strings.Index(page, `id="txn-1"`):]
	row = row[:strings.Index(row, "</tr>")]
	if strings.Contains(row, "<button") {
		t.Error("a reconciled row offers a mark; it must not (RC-3)")
	}

	w := hxPost(t, server(t, store), "/accounts/1/transactions/1/status", url.Values{
		"status": {"uncleared"}, "updated_at": {token(t, store, acct, 1)},
	})
	if !strings.Contains(w.Body.String(), "completed reconciliation") {
		t.Errorf("the refusal does not explain itself:\n%s", w.Body.String())
	}
	if got := statusOf(t, store, acct, 1); got != transaction.Reconciled {
		t.Errorf("status = %q, want reconciled", got)
	}
}

// TestMarkClearedWithoutToken: a mark that carries no version cannot be checked
// against anything, and a write that skips the check is the overwrite CO-3
// forbids.
func TestMarkClearedWithoutToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := hxPost(t, server(t, store), "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if got := statusOf(t, store, acct, 3); got != transaction.Uncleared {
		t.Errorf("status = %q: an unchecked mark was applied", got)
	}
}

// TestMarkClearedWithoutHtmx: the control is a real form, so it still works when
// the script never loaded. The answer is a redirect, not a fragment.
func TestMarkClearedWithoutHtmx(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := post(t, server(t, store), "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"}, "updated_at": {token(t, store, acct, 3)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/accounts/1#txn-3" {
		t.Errorf("Location = %q", got)
	}
	if got := statusOf(t, store, acct, 3); got != transaction.Cleared {
		t.Errorf("status = %q, want cleared", got)
	}
}

// TestMarkClearedConflictWithoutHtmx: with no fragment to swap, the conflict
// still has to be said out loud rather than dropped.
func TestMarkClearedConflictWithoutHtmx(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	stale := token(t, store, acct, 3)
	if w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"}, "updated_at": {stale},
	}); w.Code != http.StatusOK {
		t.Fatalf("first mark: %d", w.Code)
	}

	w := post(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"uncleared"}, "updated_at": {stale},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "changed in another window") {
		t.Error("the conflict page does not say what happened")
	}
}

// TestHtmxIsServedLocally: vendored, never a CDN (PL-3).
func TestHtmxIsServedLocally(t *testing.T) {
	store := open(t)

	w := get(t, server(t, store), "/static/htmx.min.js")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.Len() < 10000 {
		t.Errorf("htmx is %d bytes; that is not the library", w.Body.Len())
	}

	page := get(t, server(t, store), "/accounts/1").Body.String()
	if strings.Contains(page, "//unpkg.com") || strings.Contains(page, "cdn") {
		t.Error("the page references a CDN")
	}
}
