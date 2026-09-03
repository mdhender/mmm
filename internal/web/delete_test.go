// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestDeleteAsksFirst is RG-3: removing a transaction cannot be undone from
// inside the program, so the page shows the whole thing and asks.
func TestDeleteAsksFirst(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := get(t, server(t, store), "/accounts/1/transactions/3/delete")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		"Remove this transaction?",
		"2026-08-30",        // the date
		"Panaderia Ana",     // the payee
		"-14.75",            // the amount
		"Uncategorized",     // what it was filed under
		`name="updated_at"`, // the version this page was drawn for (CO-3)
		"Keep it",           // a way out that is not the destructive one
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the confirmation is missing %q", want)
		}
	}
	// Asking is not doing.
	if len(rows(t, store, acct)) != 3 {
		t.Error("drawing the confirmation removed the transaction")
	}
}

// TestDeleteRemovesTheTransaction is RG-2's "remove", end to end: the row is
// gone, the balances follow, and the register says what happened.
func TestDeleteRemovesTheTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	w := postFromPage(t, h, "/accounts/1/transactions/3/delete", url.Values{
		"updated_at": {token(t, store, acct, 3)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/accounts/1?removed=1" {
		t.Errorf("Location = %q, want /accounts/1?removed=1", got)
	}

	if n := len(rows(t, store, acct)); n != 2 {
		t.Fatalf("the register holds %d rows, want 2", n)
	}

	body := get(t, h, "/accounts/1?removed=1").Body.String()
	if strings.Contains(body, "Panaderia Ana") {
		t.Error("the removed transaction is still on the register")
	}
	// 3812.44 + 2480.16 - 84.17.
	if !strings.Contains(body, "6,208.43") {
		t.Error("the ending balance did not follow the removal")
	}
	if !strings.Contains(body, "was removed") {
		t.Error("the register does not say what happened")
	}
	if !strings.Contains(body, "categories themselves are untouched") {
		t.Error("the notice does not say the categories survived")
	}
}

// TestDeleteTakesTheSplitsWithIt: the parts go with the whole, and the
// categories they named do not.
func TestDeleteTakesTheSplitsWithIt(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	body := get(t, h, "/accounts/1/transactions/2/delete").Body.String()
	if !strings.Contains(body, "Split 2 ways") {
		t.Error("the confirmation does not say the transaction is split")
	}

	w := postFromPage(t, h, "/accounts/1/transactions/2/delete", url.Values{
		"updated_at": {token(t, store, acct, 2)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	// The categories are still offered on the entry form: removing a transaction
	// does not remove what it was filed under.
	register := get(t, h, "/accounts/1").Body.String()
	for _, name := range []string{"Groceries", "Household"} {
		if !strings.Contains(register, `<option value="`+name+`">`) {
			t.Errorf("removing a split transaction removed the category %q", name)
		}
	}
}

// TestDeleteRefusesAStaleToken: the reader confirmed a transaction that has
// since changed, so it is no longer the one they were shown (CO-3).
func TestDeleteRefusesAStaleToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	stale := token(t, store, acct, 3)

	// Another window marks it cleared while the confirmation sits open.
	if w := hxPost(t, h, "/accounts/1/transactions/3/status", url.Values{
		"status": {"cleared"}, "updated_at": {stale},
	}); w.Code != http.StatusOK {
		t.Fatalf("mark cleared: status = %d", w.Code)
	}

	w := postFromPage(t, h, "/accounts/1/transactions/3/delete", url.Values{"updated_at": {stale}})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "changed in another window") {
		t.Error("the refusal does not say what happened")
	}
	if !strings.Contains(body, "What to do next") {
		t.Error("the refusal does not say what to do next (RG-4)")
	}
	if len(rows(t, store, acct)) != 3 {
		t.Error("the refused removal ran anyway")
	}
}

// TestDeleteWithoutAToken: a form that arrived without the version it was made
// against is refused rather than applied, because there is nothing to compare.
func TestDeleteWithoutAToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := postFromPage(t, server(t, store), "/accounts/1/transactions/3/delete", url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400\n%s", w.Code, w.Body.String())
	}
	if len(rows(t, store, acct)) != 3 {
		t.Error("a removal with no version to check was applied")
	}
}
