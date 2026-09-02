// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// entryValues is a complete, valid entry. Tests change one field at a time so
// each failure names exactly one rule.
func entryValues() url.Values {
	return url.Values{
		"date":         {"2026-08-31"},
		"check_number": {"1043"},
		"payee":        {"Felipe Motta"},
		"memo":         {"a bottle for Sunday"},
		"category":     {"Dining"},
		"payment":      {"36.42"},
	}
}

// rows reads the register straight from the domain, so an assertion about what
// was written does not depend on how the page renders it.
func rows(t *testing.T, store *storage.Store, acct account.Account) []transaction.Entry {
	t.Helper()
	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	return reg.Entries
}

// TestEnterPayment is the whole point of the step: money leaves the account, the
// row lands with the sign the box implied, and every balance below it follows.
func TestEnterPayment(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	w := post(t, h, "/accounts/1/transactions", entryValues())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}
	// See Other, not 200: a reload after this is a GET of the register and not a
	// second transaction.
	if got := w.Header().Get("Location"); got != "/accounts/1#txn-4" {
		t.Errorf("Location = %q, want /accounts/1#txn-4", got)
	}

	entries := rows(t, store, acct)
	if len(entries) != 4 {
		t.Fatalf("register has %d rows, want 4", len(entries))
	}
	got := entries[3]
	if got.Payee != "Felipe Motta" {
		t.Errorf("payee = %q", got.Payee)
	}
	if got.Date != "2026-08-31" {
		t.Errorf("date = %q, want 2026-08-31 exactly as typed", got.Date)
	}
	if got.CheckNumber != "1043" {
		t.Errorf("check number = %q", got.CheckNumber)
	}
	if got.Memo != "a bottle for Sunday" {
		t.Errorf("memo = %q", got.Memo)
	}
	// Typed as 36.42 under Payment, stored negative.
	if d := got.Amount.Decimal(); d != "-36.42" {
		t.Errorf("amount = %s, want -36.42", d)
	}
	if got.Status != transaction.Uncleared {
		t.Errorf("status = %q, want uncleared: the bank has not shown it", got.Status)
	}
	if got.Category != "Dining" {
		t.Errorf("category = %q, want Dining", got.Category)
	}
	// 6,193.68 before this entry, less 36.42.
	if d := got.Balance.Decimal(); d != "6157.26" {
		t.Errorf("running balance = %s, want 6157.26", d)
	}
}

// TestEnterDeposit is the other direction: the same form, the other box, and the
// amount is stored positive without anyone typing a sign.
func TestEnterDeposit(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	form := entryValues()
	form.Del("payment")
	form.Set("deposit", "1200.00")
	form.Set("payee", "Acme Manufacturing")
	form.Set("category", "Salary")

	if w := post(t, server(t, store), "/accounts/1/transactions", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	entries := rows(t, store, acct)
	got := entries[len(entries)-1]
	if d := got.Amount.Decimal(); d != "1200.00" {
		t.Errorf("amount = %s, want 1200.00", d)
	}
	if d := got.Balance.Decimal(); d != "7393.68" {
		t.Errorf("running balance = %s, want 7393.68", d)
	}
}

// TestEntryRefusals covers every rule the form enforces. Each case must be
// refused with 422, must write nothing, and must say what to do next -- a
// message that only names the fault is the dead end RG-4 forbids.
func TestEntryRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(url.Values)
		want   string
	}{
		{"both boxes filled", func(v url.Values) { v.Set("deposit", "36.42") }, "not both"},
		{"neither box filled", func(v url.Values) { v.Del("payment") }, "needs an amount"},
		{"missing date", func(v url.Values) { v.Set("date", "") }, "needs a date"},
		{"date is not a date", func(v url.Values) { v.Set("date", "31/08/2026") }, "YYYY-MM-DD"},
		{"date does not exist", func(v url.Values) { v.Set("date", "2026-02-31") }, "not a calendar date"},
		{"missing payee", func(v url.Values) { v.Set("payee", "   ") }, "needs a payee"},
		{"amount is not a number", func(v url.Values) { v.Set("payment", "thirty") }, "not an amount"},
		{"amount too precise for USD", func(v url.Values) { v.Set("payment", "36.425") }, "more precise"},
		{"amount carries a sign", func(v url.Values) { v.Set("payment", "-36.42") }, "without a sign"},
		{"amount is zero", func(v url.Values) { v.Set("payment", "0.00") }, "would not change the balance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := open(t)
			acct := seed(t, store)
			before := len(rows(t, store, acct))

			form := entryValues()
			tc.change(form)
			w := post(t, server(t, store), "/accounts/1/transactions", form)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("page does not explain the refusal; wanted %q in:\n%s", tc.want, body)
			}
			if !strings.Contains(body, "press Add again") {
				t.Error("refusal does not say what to do next (RG-4)")
			}
			if after := len(rows(t, store, acct)); after != before {
				t.Errorf("register has %d rows, want %d: a refused entry wrote something", after, before)
			}
		})
	}
}

// TestRefusedEntryKeepsWhatWasTyped: the page comes back with the entry still in
// it. Making somebody retype a transaction they already typed is how a register
// gets abandoned.
func TestRefusedEntryKeepsWhatWasTyped(t *testing.T) {
	store := open(t)
	seed(t, store)

	form := entryValues()
	form.Set("payment", "thirty")
	body := post(t, server(t, store), "/accounts/1/transactions", form).Body.String()

	for _, want := range []string{
		`value="2026-08-31"`,
		`value="1043"`,
		`value="Felipe Motta"`,
		`value="a bottle for Sunday"`,
		`value="Dining"`,
		`value="thirty"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the returned form is missing %s", want)
		}
	}

	// And the register is still on the page, with its balances.
	if !strings.Contains(body, "Cleared balance") {
		t.Error("the refused entry lost the register it was typed on")
	}
}

// TestEntryReusesCategoryCaseInsensitively guards the thing that would quietly
// split a year of spending in two.
func TestEntryReusesCategoryCaseInsensitively(t *testing.T) {
	store := open(t)
	seed(t, store) // seeds "Groceries" and "Household"

	form := entryValues()
	form.Set("category", "groceries")
	if w := post(t, server(t, store), "/accounts/1/transactions", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	categories, err := category.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var names []string
	for _, c := range categories {
		names = append(names, c.Name)
	}
	if len(categories) != 2 {
		t.Fatalf("categories = %v, want the original two: a spelling made a second one", names)
	}
	// Ensure does not rewrite the stored spelling.
	if names[0] != "Groceries" {
		t.Errorf("stored spelling = %q, want Groceries", names[0])
	}
}

// TestEntryWithoutCategoryIsUncategorized: no category is a normal state, not an
// error, and the register says so rather than showing a blank cell.
func TestEntryWithoutCategoryIsUncategorized(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	form := entryValues()
	form.Set("category", "")
	if w := post(t, server(t, store), "/accounts/1/transactions", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", w.Code)
	}

	entries := rows(t, store, acct)
	got := entries[len(entries)-1]
	if got.SplitCount != 0 {
		t.Errorf("split count = %d, want 0: a blank category should write no split", got.SplitCount)
	}
	if got.Category != "" {
		t.Errorf("category = %q, want empty", got.Category)
	}
	if body := get(t, server(t, store), "/accounts/1").Body.String(); !strings.Contains(body, "Uncategorized") {
		t.Error("the register does not label the row Uncategorized")
	}
}

// TestEntryFormOnEmptyRegister: an account with no transactions is exactly the
// one that most needs the form, so it must not live inside the table.
func TestEntryFormOnEmptyRegister(t *testing.T) {
	store := open(t)
	if _, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	body := get(t, server(t, store), "/accounts/1").Body.String()
	if !strings.Contains(body, "This register has no transactions yet") {
		t.Fatal("not the empty register page")
	}
	if !strings.Contains(body, `action="/accounts/1/transactions"`) {
		t.Error("an empty register offers no way to enter the first transaction")
	}
}

// TestEntryDateDefaultsToToday saves the common case a keystroke, and must be a
// calendar date rather than anything carrying a timezone (ST-8).
func TestEntryDateDefaultsToToday(t *testing.T) {
	store := open(t)
	seed(t, store)

	body := get(t, server(t, store), "/accounts/1").Body.String()
	today := time.Now().Format(transaction.DateLayout)
	if !strings.Contains(body, `value="`+today+`"`) {
		t.Errorf("the date box does not default to %s", today)
	}
}

// TestEntryRefusedOnClosedAccount: a closed account is a finished record.
func TestEntryRefusedOnClosedAccount(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	closeAccount(t, store, acct.ID, "2026-08-31")

	w := post(t, server(t, store), "/accounts/1/transactions", entryValues())
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, "closed") {
		t.Error("the page does not say the account is closed")
	}
	if n := len(rows(t, store, acct)); n != 3 {
		t.Errorf("register has %d rows, want 3: an entry reached a closed account", n)
	}
}

// closeAccount closes an account by writing the column directly. Closing has no
// domain API yet; when it grows one, this helper goes away.
func closeAccount(t *testing.T, store *storage.Store, id int64, on string) {
	t.Helper()
	conn, err := store.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`UPDATE accounts SET closed_at = $on WHERE id = $id;`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	stmt.SetText("$on", on)
	stmt.SetInt64("$id", id)
	if _, err := stmt.Step(); err != nil {
		t.Fatalf("close account: %v", err)
	}
	if err := stmt.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
}
