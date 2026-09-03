// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
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

// splitLine is one line of the split editor as a test types it. It mirrors the
// three boxes on a row, not the package's own type.
type splitLine struct{ category, memo, amount string }

// splitValues is a form in split mode: the transaction's own boxes, a line per
// part, and the version it was filled in against.
func splitValues(t *testing.T, store *storage.Store, acct account.Account, id int64, payment string, lines ...splitLine) url.Values {
	t.Helper()
	v := url.Values{
		"date":       {"2026-08-27"},
		"payee":      {"Riba Smith"},
		"payment":    {payment},
		"split":      {"1"},
		"updated_at": {token(t, store, acct, id)},
	}
	for _, line := range lines {
		v.Add("split_category", line.category)
		v.Add("split_memo", line.memo)
		v.Add("split_amount", line.amount)
	}
	return v
}

// detailOf reads one transaction with its parts, straight from the domain.
func detailOf(t *testing.T, store *storage.Store, acct account.Account, id int64) transaction.Detail {
	t.Helper()
	d, err := transaction.Get(t.Context(), store, acct, id)
	if err != nil {
		t.Fatalf("Get %d: %v", id, err)
	}
	return d
}

// checkSplits compares a transaction's stored parts, in order, against what the
// form was meant to have written. Order counts: it is the order the household
// typed the lines in.
func checkSplits(t *testing.T, store *storage.Store, acct account.Account, id int64, want []transaction.SplitDetail) {
	t.Helper()
	got := detailOf(t, store, acct, id).Splits
	if len(got) != len(want) {
		t.Fatalf("%d parts, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Category != want[i].Category || got[i].Memo != want[i].Memo ||
			got[i].Amount.Amount() != want[i].Amount.Amount() {
			t.Errorf("part %d is %q %q %s, want %q %q %s", i+1,
				got[i].Category, got[i].Memo, got[i].Amount.Decimal(),
				want[i].Category, want[i].Memo, want[i].Amount.Decimal())
		}
	}
}

// splitRow is one row of the splits table as SQL sees it, which is the only way
// to tell a NULL category from a category named "".
type splitRow struct {
	isNull bool
	memo   string
}

func splitRows(t *testing.T, store *storage.Store, txnID int64) []splitRow {
	t.Helper()

	conn, err := store.Conn(t.Context())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`
SELECT category_id IS NULL AS is_null, memo
  FROM splits WHERE transaction_id = $transaction_id ORDER BY id;`)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	stmt.SetInt64("$transaction_id", txnID)
	defer stmt.Reset()

	var rows []splitRow
	for {
		hasRow, err := stmt.Step()
		if err != nil {
			t.Fatalf("Step: %v", err)
		}
		if !hasRow {
			return rows
		}
		rows = append(rows, splitRow{isNull: stmt.GetInt64("is_null") == 1, memo: stmt.GetText("memo")})
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

// TestSplitATransaction is RG-2's "split", end to end: a transaction with one
// category is divided among three, and the register says so.
func TestSplitATransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	// The uncategorized purchase, given a category and then divided.
	if w := post(t, h, "/accounts/1/transactions/3", editValues(t, store, acct)); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	form := splitValues(t, store, acct, 3, "15.75",
		splitLine{"Groceries", "bread", "9.00"},
		splitLine{"Household", "", "4.75"},
		splitLine{"Dining", "coffee", "2.00"})
	if w := post(t, h, "/accounts/1/transactions/3", form); w.Code != http.StatusSeeOther {
		t.Fatalf("split status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	if got := entryOf(t, store, acct, 3); got.SplitCount != 3 || !got.IsSplit() {
		t.Fatalf("the transaction has %d parts, want 3", got.SplitCount)
	}
	// Every part carries the transaction's sign, which is ADR 1's rule: the
	// household typed three unsigned amounts and the payment stayed a payment.
	want := []transaction.SplitDetail{
		{Split: transaction.Split{Amount: usd(t, "-9.00"), Memo: "bread"}, Category: "Groceries"},
		{Split: transaction.Split{Amount: usd(t, "-4.75")}, Category: "Household"},
		{Split: transaction.Split{Amount: usd(t, "-2.00"), Memo: "coffee"}, Category: "Dining"},
	}
	checkSplits(t, store, acct, 3, want)

	// The register names none of the three, because naming the first would
	// suggest the whole amount went there.
	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, "— Split —") {
		t.Error("the register does not say the transaction is split")
	}
}

// TestSplitFormOpensOnTheParts: a transaction already divided comes up in the
// mode that can show every part, with the tally under them.
func TestSplitFormOpensOnTheParts(t *testing.T) {
	store := open(t)
	seed(t, store)

	body := get(t, server(t, store), "/accounts/1/transactions/2/edit").Body.String()
	for _, want := range []string{
		`name="split" value="1"`, // the mode is a form field, not browser state
		`name="split_category"`,  // a line per part
		`value="Groceries"`,      // ... named
		`value="71.22"`,          // ... and unsigned, as it is typed
		`value="Household"`,
		`value="12.95"`,
		"Unassigned", // the tally
		"Add a line",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the split form is missing %q", want)
		}
	}
	// The one-category box is not offered alongside the lines: it could hold
	// only the first of them.
	if strings.Contains(body, `name="category"`) {
		t.Error("the form offers one category box for a transaction split two ways")
	}
	// 84.17 assigned, nothing left over.
	if !strings.Contains(body, "0.00") {
		t.Error("the tally does not show a fully assigned transaction")
	}
}

// TestAPartTheOneBoxCannotShowOpensTheEditor: a transaction with one part is
// ordinarily the plain form's business, but a part with a memo of its own, or
// with no category, is not something one box can hold -- and a box that cannot
// show a part would remove it on the next Save.
func TestAPartTheOneBoxCannotShowOpensTheEditor(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name string
		line splitLine
	}{
		{"a part with a memo", splitLine{"Groceries", "half the trolley", "84.17"}},
		{"a part with no category", splitLine{"", "not sure yet", "84.17"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := splitValues(t, store, acct, 2, "84.17", tt.line)
			if w := post(t, h, "/accounts/1/transactions/2", form); w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
			}
			if got := entryOf(t, store, acct, 2); got.SplitCount != 1 {
				t.Fatalf("the transaction has %d parts, want 1", got.SplitCount)
			}

			body := get(t, h, "/accounts/1/transactions/2/edit").Body.String()
			if !strings.Contains(body, `name="split_category"`) {
				t.Error("the form opened plain, where the part could not be shown")
			}
			if tt.line.memo != "" && !strings.Contains(body, tt.line.memo) {
				t.Error("the part's memo is not on the form")
			}
		})
	}
}

// TestUnsplitATransaction is the way back, and it needs no control: leave one
// line filled and the transaction has one category again.
func TestUnsplitATransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	form := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Groceries", "", "84.17"},
		splitLine{"Household", "", ""})
	if w := post(t, h, "/accounts/1/transactions/2", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	got := entryOf(t, store, acct, 2)
	if got.SplitCount != 1 || got.Category != "Groceries" || got.IsSplit() {
		t.Fatalf("the transaction has %d parts under %q", got.SplitCount, got.Category)
	}
	// And it opens plain next time, which is the whole of the way back.
	body := get(t, h, "/accounts/1/transactions/2/edit").Body.String()
	if !strings.Contains(body, `name="category"`) {
		t.Error("a transaction with one category does not open on the plain form")
	}
	if strings.Contains(body, `name="split_category"`) {
		t.Error("a transaction with one category opens on the split editor")
	}
}

// TestSplitToNoLinesLeavesItUncategorized: clearing every line is a normal
// state, not an error, and the categories the transaction named are not removed
// with it -- they belong to the household, not to one transaction.
func TestSplitToNoLinesLeavesItUncategorized(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	form := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Groceries", "", ""},
		splitLine{"Household", "", ""})
	if w := post(t, h, "/accounts/1/transactions/2", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	if got := entryOf(t, store, acct, 2); got.SplitCount != 0 || got.Category != "" {
		t.Errorf("the transaction has %d parts under %q, want none", got.SplitCount, got.Category)
	}
	categories, err := category.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(categories) != 2 {
		t.Errorf("the household now has %d categories, want the 2 it started with", len(categories))
	}
}

// TestSplitRefusesARemainder is the gap, said in all three numbers and written
// nowhere: the parent keeps its amount, which is what one immediate transaction
// buys.
func TestSplitRefusesARemainder(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name  string
		lines []splitLine
		want  []string
	}{
		{"under", []splitLine{{"Groceries", "", "70.00"}, {"Household", "", "10.00"}},
			[]string{"add up to 80.00", "is 84.17", "4.17 is still unassigned"}},
		{"over", []splitLine{{"Groceries", "", "80.00"}, {"Household", "", "10.00"}},
			[]string{"add up to 90.00", "is 84.17", "assign 5.83 more"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := post(t, h, "/accounts/1/transactions/2",
				splitValues(t, store, acct, 2, "84.17", tt.lines...))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422\n%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("the refusal does not say %q", want)
				}
			}
			// Nothing at all was written: not the parts, and not the amount the
			// form was trying to divide.
			got := entryOf(t, store, acct, 2)
			if got.Amount.Decimal() != "-84.17" || got.SplitCount != 2 {
				t.Errorf("the transaction is now %s in %d parts", got.Amount.Decimal(), got.SplitCount)
			}
			checkSplits(t, store, acct, 2, []transaction.SplitDetail{
				{Split: transaction.Split{Amount: usd(t, "-71.22")}, Category: "Groceries"},
				{Split: transaction.Split{Amount: usd(t, "-12.95")}, Category: "Household"},
			})
		})
	}
}

// TestSplitLineWithNoCategoryStoresNull is ADR 1's promise being kept: the
// nullable column exists for the line a reader has not decided on yet.
//
// The row is read through SQL rather than through the package, because what is
// being checked is that a NULL was stored and not the empty string or a
// placeholder category.
func TestSplitLineWithNoCategoryStoresNull(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := post(t, server(t, store), "/accounts/1/transactions/2",
		splitValues(t, store, acct, 2, "84.17",
			splitLine{"Groceries", "", "60.00"},
			splitLine{"", "not sure yet", "24.17"}))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	nulls := 0
	for _, row := range splitRows(t, store, 2) {
		if row.isNull {
			nulls++
		}
	}
	if nulls != 1 {
		t.Errorf("%d parts stored a NULL category, want 1", nulls)
	}
	// And it reads back as a part with no name rather than as a missing part.
	got := detailOf(t, store, acct, 2)
	if len(got.Splits) != 2 || got.Splits[1].Category != "" || got.Splits[1].CategoryID != 0 {
		t.Errorf("the second part reads back as %+v", got.Splits)
	}
}

// TestAddALineAnswersWithTheFormAndWritesNothing. The button is a real submit,
// so it works with no script; it is answered with the form, one line longer,
// and the transaction is untouched.
func TestAddALineAnswersWithTheFormAndWritesNothing(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	before := token(t, store, acct, 2)
	form := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Groceries", "", "71.22"},
		splitLine{"Household", "", "12.95"})
	form.Set("action", "add-line")

	w := post(t, h, "/accounts/1/transactions/2", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if got := strings.Count(body, `name="split_amount"`); got != 3 {
		t.Errorf("the form came back with %d lines, want 3", got)
	}
	if !strings.Contains(body, `value="71.22"`) || !strings.Contains(body, `value="Household"`) {
		t.Error("the form came back without the lines that were already on it")
	}

	// Nothing was written, and the token did not move: the reader's next Save is
	// still made against the version they opened.
	if got := token(t, store, acct, 2); got != before {
		t.Errorf("the version moved to %s: adding a line wrote something", got)
	}
	if got := entryOf(t, store, acct, 2); got.SplitCount != 2 {
		t.Errorf("the transaction now has %d parts", got.SplitCount)
	}
	if !strings.Contains(body, before) {
		t.Error("the form came back without the version it was filled in against")
	}
	// Without htmx the answer is the whole page, which is what makes the button
	// work with no script at all.
	if !strings.Contains(body, "<html") {
		t.Error("a plain press was answered with a fragment rather than a page")
	}

	// With htmx it is the form alone, swapped in place.
	hx := hxPost(t, h, "/accounts/1/transactions/2", form)
	if hx.Code != http.StatusOK {
		t.Fatalf("htmx status = %d, want 200", hx.Code)
	}
	fragment := hx.Body.String()
	if strings.Contains(fragment, "<html") || strings.Contains(fragment, "Back to the register") {
		t.Error("the htmx answer is a page rather than the form")
	}
	if !strings.HasPrefix(strings.TrimSpace(fragment), "<form") {
		t.Errorf("the htmx answer does not begin with the form it replaces:\n%s", fragment)
	}
	if got := strings.Count(fragment, `name="split_amount"`); got != 3 {
		t.Errorf("the swapped form has %d lines, want 3", got)
	}
	if got := entryOf(t, store, acct, 2); got.SplitCount != 2 {
		t.Errorf("the htmx press wrote %d parts", got.SplitCount)
	}
}

// TestSplitThisTransactionOpensTheEditor: the plain form's button, which writes
// nothing either and brings the category already typed onto the first line.
func TestSplitThisTransactionOpensTheEditor(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	form := editValues(t, store, acct)
	form.Set("action", "split")
	w := post(t, server(t, store), "/accounts/1/transactions/3", form)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, `name="split_category"`) {
		t.Fatal("the form did not come back in split mode")
	}
	if !strings.Contains(body, `value="Groceries"`) || !strings.Contains(body, `value="15.75"`) {
		t.Error("the first line did not take the category and the amount already typed")
	}
	if got := strings.Count(body, `name="split_amount"`); got != 2 {
		t.Errorf("the editor opened with %d lines, want 2: one filled and one to divide into", got)
	}
	if got := entryOf(t, store, acct, 3); got.Category != "" || got.SplitCount != 0 {
		t.Errorf("pressing Split wrote %d parts", got.SplitCount)
	}
}

// TestSplitRefusesABadLine: the rules of the entry form, on a line, naming the
// line rather than a box that is not there.
func TestSplitRefusesABadLine(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name   string
		amount string
		want   string
	}{
		{"a signed amount", "-12.95", "without a sign"},
		{"not an amount", "twelve", "is not an amount"},
		{"too precise", "12.955", "more precise than this account can hold"},
		{"zero", "0.00", "Line 2 is for zero"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := post(t, h, "/accounts/1/transactions/2",
				splitValues(t, store, acct, 2, "84.17",
					splitLine{"Groceries", "", "71.22"},
					splitLine{"Household", "", tt.amount}))
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422\n%s", w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("the refusal does not say %q:\n%s", tt.want, body)
			}
			if !strings.Contains(body, "press Save again") {
				t.Error("the refusal tells the reader to press a button that is not on this page")
			}
			// The lines come back as they were typed, the bad one included.
			if !strings.Contains(body, `value="71.22"`) {
				t.Error("the reader's other lines were thrown away")
			}
			if got := entryOf(t, store, acct, 2); got.SplitCount != 2 {
				t.Errorf("a refused change was written: %d parts", got.SplitCount)
			}
		})
	}
}

// TestEditChangesTheAmountOfASplitTransaction. The amount of a split
// transaction is editable now, because the lines can follow it: this is what
// TestEditRefusesANewAmountOnASplitTransaction pinned before there was an
// editor.
func TestEditChangesTheAmountOfASplitTransaction(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	form := splitValues(t, store, acct, 2, "90.00",
		splitLine{"Groceries", "", "77.05"},
		splitLine{"Household", "", "12.95"})
	if w := post(t, h, "/accounts/1/transactions/2", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	got := entryOf(t, store, acct, 2)
	if got.Amount.Decimal() != "-90.00" || got.SplitCount != 2 {
		t.Errorf("the transaction is %s in %d parts, want -90.00 in 2", got.Amount.Decimal(), got.SplitCount)
	}
	checkSplits(t, store, acct, 2, []transaction.SplitDetail{
		{Split: transaction.Split{Amount: usd(t, "-77.05")}, Category: "Groceries"},
		{Split: transaction.Split{Amount: usd(t, "-12.95")}, Category: "Household"},
	})
}

// TestSplitKeepsTheRestOfTheTransaction: the parts are not the only thing on the
// page, and changing the payee of a transaction split two ways leaves the two
// alone -- which is what the form did before there was an editor, and still
// does now that there is one.
func TestSplitKeepsASplitTransactionWhole(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	form := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Groceries", "", "71.22"},
		splitLine{"Household", "", "12.95"})
	form.Set("payee", "Riba Smith SA")
	form.Set("memo", "weekly shop")
	if w := post(t, h, "/accounts/1/transactions/2", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	got := entryOf(t, store, acct, 2)
	if got.Payee != "Riba Smith SA" || got.Memo != "weekly shop" {
		t.Errorf("the change did not land: %+v", got)
	}
	if got.SplitCount != 2 {
		t.Errorf("the transaction now has %d parts, want 2: editing flattened it", got.SplitCount)
	}
}

// TestTheSplitEditorSaysEmpty guards a word this program cannot afford to use
// twice.
//
// "Cleared" is a status a transaction is in -- the bank showed it -- so "clear
// the box" would be the same word for a mark against the bank and for taking
// text out of an input. The glossary settles it: emptying is "empty", and clear
// keeps its one meaning. The page and its refusals are checked together, because
// a message is as much of the interface as the note under the form is.
//
// The test's own name is part of the fixture: an in-memory store is named after
// it and BK-3 prints that name in the footer, so a test called ...SaysClear
// fails itself.
func TestTheSplitEditorSaysEmpty(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	// Anything but the status word. Taking "cleared" out first also takes
	// "uncleared" with it, so what is left is the verb this page must not use.
	looseClear := func(body string) bool {
		return strings.Contains(strings.ReplaceAll(strings.ToLower(body), "cleared", ""), "clear")
	}

	pages := map[string]string{
		"the editor": get(t, h, "/accounts/1/transactions/2/edit").Body.String(),
		"a remainder": post(t, h, "/accounts/1/transactions/2",
			splitValues(t, store, acct, 2, "84.17", splitLine{"Groceries", "", "70.00"})).Body.String(),
		"a line for zero": post(t, h, "/accounts/1/transactions/2",
			splitValues(t, store, acct, 2, "84.17", splitLine{"Groceries", "", "0.00"})).Body.String(),
	}
	for name, body := range pages {
		if looseClear(body) {
			t.Errorf("%s says clear where it means empty", name)
		}
	}

	// And it does say the word it is supposed to say.
	if !strings.Contains(pages["the editor"], "Empty the Amount box") {
		t.Error("the note does not tell the reader how a line is taken away")
	}
	if !strings.Contains(pages["the editor"], "saved to the checkbook only if it has an Amount") {
		t.Error("the note does not say what makes a line a line")
	}
}

// TestSplitRefusesAStaleToken is CO-3 over a whole set of parts: the second tab
// is refused, the first tab's lines stand, and what is stored is one of the two
// divisions rather than a mixture of them.
func TestSplitRefusesAStaleToken(t *testing.T) {
	store := open(t)
	acct := seed(t, store)
	h := server(t, store)

	stale := token(t, store, acct, 2) // what both tabs read

	first := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Groceries", "", "84.17"})
	if w := post(t, h, "/accounts/1/transactions/2", first); w.Code != http.StatusSeeOther {
		t.Fatalf("first status = %d, want 303\n%s", w.Code, w.Body.String())
	}

	second := splitValues(t, store, acct, 2, "84.17",
		splitLine{"Household", "", "40.00"},
		splitLine{"Dining", "", "44.17"})
	second.Set("updated_at", stale)
	w := post(t, h, "/accounts/1/transactions/2", second)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409\n%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "changed in another window") {
		t.Error("the refusal does not say what happened")
	} else if !strings.Contains(body, `value="Dining"`) {
		t.Error("the reader's lines were thrown away")
	}

	// The first tab's division stands, whole: not one of its lines and one of
	// the second's.
	checkSplits(t, store, acct, 2, []transaction.SplitDetail{
		{Split: transaction.Split{Amount: usd(t, "-84.17")}, Category: "Groceries"},
	})
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
