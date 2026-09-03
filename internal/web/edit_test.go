// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// entryOf returns one register row, so a test can check what was actually
// stored rather than what the page said.
func entryOf(t *testing.T, store *storage.Store, acct account.Account, id int64) transaction.Entry {
	t.Helper()
	for _, e := range rows(t, store, acct) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no transaction %d in the register", id)
	return transaction.Entry{}
}

// editValues is a complete, valid change to the seed's third transaction: the
// uncleared, uncategorized purchase.
func editValues(t *testing.T, store *storage.Store, acct account.Account) url.Values {
	t.Helper()
	return url.Values{
		"date":         {"2026-08-31"},
		"check_number": {"1042"},
		"payee":        {"Panaderia Ana SA"},
		"memo":         {"bread and coffee"},
		"category":     {"Groceries"},
		"payment":      {"15.75"},
		"updated_at":   {token(t, store, acct, 3)},
	}
}

// TestEditFormShowsTheTransaction: the form comes up filled in, with the amount
// back in the box that says which way the money went.
func TestEditFormShowsTheTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := get(t, server(t, store), "/accounts/1/transactions/3/edit")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		`value="2026-08-30"`,      // the date, exactly as stored (ST-8)
		`value="Panaderia Ana"`,   // the payee
		`name="payment"`,          // the two boxes of a paper register
		`value="14.75"`,           // the amount, without the stored sign
		`name="updated_at"`,       // the token that guards the write (CO-3)
		`Save`,                    // the button the messages name
		"Remove this transaction", // the way to the removal page
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the edit form is missing %q", want)
		}
	}
	// A payment is not also a deposit: the empty box must come up empty.
	if strings.Contains(body, `name="deposit" value="14.75"`) {
		t.Error("the amount is in both boxes")
	}
	// The token is the row's own, not a placeholder.
	if !strings.Contains(body, storage.FormatTime(entryOf(t, store, acct, 3).UpdatedAt)) {
		t.Error("the form does not carry the transaction's current version")
	}
}

// TestEditChangesTheTransaction is RG-2's "change", end to end: every field, and
// the register's running balance following it.
func TestEditChangesTheTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	w := post(t, h, "/accounts/1/transactions/3", editValues(t, store, acct))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/accounts/1#txn-3" {
		t.Errorf("Location = %q, want /accounts/1#txn-3", got)
	}

	got := entryOf(t, store, acct, 3)
	if got.Date != "2026-08-31" || got.Payee != "Panaderia Ana SA" || got.Memo != "bread and coffee" ||
		got.CheckNumber != "1042" || got.Amount.Decimal() != "-15.75" || got.Category != "Groceries" {
		t.Errorf("stored transaction is %+v", got)
	}
	// Clearing is not an editable field: it is a fact about the bank (RC-3).
	if got.Status != transaction.Uncleared {
		t.Errorf("status = %q, want uncleared", got.Status)
	}

	// And the page the reader lands on agrees. 3812.44 + 2480.16 - 84.17 - 15.75.
	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, "6,192.68") {
		t.Error("the ending balance did not follow the change")
	}
}

// TestEditAddsAndRemovesACategory: the category box writes the split, and
// clearing it removes it. Uncategorized is a normal state, not an error.
func TestEditAddsAndRemovesACategory(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	form := editValues(t, store, acct)
	if w := post(t, h, "/accounts/1/transactions/3", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	if got := entryOf(t, store, acct, 3); got.Category != "Groceries" || got.SplitCount != 1 {
		t.Fatalf("category = %q with %d splits, want Groceries with 1", got.Category, got.SplitCount)
	}

	form = editValues(t, store, acct)
	form.Set("updated_at", token(t, store, acct, 3))
	form.Set("category", "")
	if w := post(t, h, "/accounts/1/transactions/3", form); w.Code != http.StatusSeeOther {
		t.Fatalf("second status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	got := entryOf(t, store, acct, 3)
	if got.Category != "" || got.SplitCount != 0 {
		t.Errorf("category = %q with %d splits, want none", got.Category, got.SplitCount)
	}
}

// TestEditKeepsASplitTransactionWhole. There is no split editor in this release,
// so the form shows the parts rather than offering them, and everything else can
// still be changed without flattening what the household recorded.
func TestEditKeepsASplitTransactionWhole(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	body := get(t, h, "/accounts/1/transactions/2/edit").Body.String()
	if !strings.Contains(body, "Split") {
		t.Error("the form does not say the transaction is split")
	}
	if strings.Contains(body, `name="category"`) {
		t.Error("the form offers one category box for a transaction split two ways")
	}
	if !strings.Contains(body, "readonly") {
		t.Error("the amount is offered for editing on a split transaction")
	}

	// A change to the rest of it goes through, and the parts are untouched.
	w := post(t, h, "/accounts/1/transactions/2", url.Values{
		"date":       {"2026-08-27"},
		"payee":      {"Riba Smith SA"},
		"memo":       {"weekly shop"},
		"payment":    {"84.17"},
		"updated_at": {token(t, store, acct, 2)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	got := entryOf(t, store, acct, 2)
	if got.Payee != "Riba Smith SA" || got.Memo != "weekly shop" {
		t.Errorf("the change did not land: %+v", got)
	}
	if got.SplitCount != 2 {
		t.Errorf("the transaction now has %d splits, want 2: editing flattened it", got.SplitCount)
	}
}

// TestEditRefusesANewAmountOnASplitTransaction: the parts would no longer add
// up, and this release cannot edit them, so it says so rather than breaking the
// invariant or silently keeping the old amount.
func TestEditRefusesANewAmountOnASplitTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := post(t, server(t, store), "/accounts/1/transactions/2", url.Values{
		"date":       {"2026-08-27"},
		"payee":      {"Riba Smith"},
		"payment":    {"90.00"},
		"updated_at": {token(t, store, acct, 2)},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422\n%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "divided among 2 categories") {
		t.Errorf("the refusal does not say why:\n%s", body)
	}
	if got := entryOf(t, store, acct, 2); got.Amount.Decimal() != "-84.17" {
		t.Errorf("amount = %s, want -84.17: the refused change was applied", got.Amount.Decimal())
	}
}

// TestEditRefusesAStaleToken is CO-3 through the browser: two tabs, and the
// second is told what the transaction now says rather than overwriting it.
func TestEditRefusesAStaleToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	stale := token(t, store, acct, 3) // what both tabs read

	first := editValues(t, store, acct)
	first.Set("payee", "Panaderia Ana")
	if w := post(t, h, "/accounts/1/transactions/3", first); w.Code != http.StatusSeeOther {
		t.Fatalf("first status = %d, want 303", w.Code)
	}

	second := editValues(t, store, acct)
	second.Set("updated_at", stale)
	second.Set("payee", "Somewhere Else")
	w := post(t, h, "/accounts/1/transactions/3", second)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "changed in another window") {
		t.Error("the refusal does not say what happened")
	}
	if !strings.Contains(body, "Panaderia Ana") {
		t.Error("the refusal does not say what the transaction now reads")
	}
	// The reader's own work is still in the boxes, with a version they can save
	// over: told, not discarded.
	if !strings.Contains(body, `value="Somewhere Else"`) {
		t.Error("the reader's typing was thrown away")
	}
	if got := entryOf(t, store, acct, 3); got.Payee != "Panaderia Ana" {
		t.Errorf("payee = %q: the refused write was applied anyway", got.Payee)
	}
}

// TestEditRefusesAReconciledTransaction: RC-3, on the form as well as on the
// write, so the offer is never made.
func TestEditRefusesAReconciledTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name string
		send func() *http.Response
	}{
		{"the form", func() *http.Response { return get(t, h, "/accounts/1/transactions/1/edit").Result() }},
		{"the change", func() *http.Response {
			return post(t, h, "/accounts/1/transactions/1", url.Values{
				"date": {"2026-08-14"}, "payee": {"Acme"}, "deposit": {"2480.16"},
				"updated_at": {token(t, store, acct, 1)},
			}).Result()
		}},
		{"the removal page", func() *http.Response { return get(t, h, "/accounts/1/transactions/1/delete").Result() }},
		{"the removal", func() *http.Response {
			return postFromPage(t, h, "/accounts/1/transactions/1/delete", url.Values{
				"updated_at": {token(t, store, acct, 1)},
			}).Result()
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := tt.send()
			if res.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409", res.StatusCode)
			}
		})
	}

	if got := entryOf(t, store, acct, 1); got.Payee != "Acme Manufacturing" {
		t.Errorf("payee = %q: a reconciled transaction was changed", got.Payee)
	}
}

// TestEditFormRefusesWhatItCannotWrite: the same rules the entry form applies,
// named by the button that is actually on this page.
func TestEditFormRefusesBadInput(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"no payee", "payee", "", "needs a payee"},
		{"not a date", "date", "2026-02-30", "not a calendar date"},
		{"a signed amount", "payment", "-15.75", "without a sign"},
		{"no amount", "payment", "", "needs an amount"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := editValues(t, store, acct)
			form.Set(tt.field, tt.value)
			w := post(t, h, "/accounts/1/transactions/3", form)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("the refusal does not say %q:\n%s", tt.want, body)
			}
			// The messages name the button this page has, not the register's.
			if !strings.Contains(body, "press Save again") {
				t.Error("the refusal tells the reader to press a button that is not on this page")
			}
			if got := entryOf(t, store, acct, 3); got.Payee != "Panaderia Ana" {
				t.Errorf("payee = %q: a refused change was written", got.Payee)
			}
		})
	}
}

// TestRegisterOffersEdit: the row carries the link, and a reconciled row does
// not.
func TestRegisterOffersEdit(t *testing.T) {
	store := open(t)
	seed(t, store)

	body := get(t, server(t, store), "/accounts/1").Body.String()
	if !strings.Contains(body, "/accounts/1/transactions/3/edit") {
		t.Error("the register does not offer to edit a transaction")
	}
	if strings.Contains(body, "/accounts/1/transactions/1/edit") {
		t.Error("the register offers to edit a reconciled transaction (RC-3)")
	}
}

// TestEditAddressThatNamesNothing: a bookmark, or a typed address.
func TestEditAddressThatNamesNothing(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := server(t, store)

	for _, path := range []string{
		"/accounts/1/transactions/99/edit",
		"/accounts/1/transactions/nine/edit",
		"/accounts/1/transactions/99/delete",
	} {
		w := get(t, h, path)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "What to do next") {
			t.Errorf("%s: the page does not say what to do next (RG-4)", path)
		}
	}
}
