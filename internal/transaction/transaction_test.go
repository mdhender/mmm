// Copyright (c) 2026 Michael D Henderson.

package transaction_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
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

func usd(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.ParseDecimal(amount, money.USD)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", amount, err)
	}
	return m
}

// checking returns a store and a USD checking account opened at balance.
func checking(t *testing.T, balance string) (*storage.Store, account.Account) {
	t.Helper()
	store := open(t)
	acct, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD,
		OpeningBalance: usd(t, balance),
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return store, acct
}

// TestRegisterRunningBalance is the arithmetic the whole screen rests on: rows
// in date order, each balance the one before it plus this amount, and the ending
// balance the last of them.
func TestRegisterRunningBalance(t *testing.T) {
	store, acct := checking(t, "100.00")

	// Entered out of order on purpose: the register orders by date, not by
	// insertion.
	for _, n := range []transaction.New{
		{Date: "2026-08-29", Payee: "Felipe Motta", Amount: usd(t, "-36.42")},
		{Date: "2026-08-14", Payee: "Acme", Amount: usd(t, "2480.16"), Status: transaction.Reconciled},
		{Date: "2026-08-27", Payee: "Riba Smith", Amount: usd(t, "-84.17"), Status: transaction.Cleared},
	} {
		if _, err := transaction.Create(t.Context(), store, acct, n); err != nil {
			t.Fatalf("Create(%s): %v", n.Payee, err)
		}
	}

	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}

	want := []struct {
		date    string
		balance string
	}{
		{"2026-08-14", "2580.16"},
		{"2026-08-27", "2495.99"},
		{"2026-08-29", "2459.57"},
	}
	if len(reg.Entries) != len(want) {
		t.Fatalf("register has %d entries, want %d", len(reg.Entries), len(want))
	}
	for i, w := range want {
		if got := reg.Entries[i].Date; got != w.date {
			t.Errorf("entry %d date = %s, want %s", i, got, w.date)
		}
		if got := reg.Entries[i].Balance.Decimal(); got != w.balance {
			t.Errorf("entry %d balance = %s, want %s", i, got, w.balance)
		}
	}

	if got := reg.Ending.Decimal(); got != "2459.57" {
		t.Errorf("ending balance = %s, want 2459.57", got)
	}
	// Cleared and reconciled have both reached the bank; the uncleared one has
	// not, and the difference between the two balances is what says so.
	if got := reg.Cleared.Decimal(); got != "2495.99" {
		t.Errorf("cleared balance = %s, want 2495.99", got)
	}
	if reg.UnclearedCount != 1 {
		t.Errorf("uncleared count = %d, want 1", reg.UnclearedCount)
	}
}

// TestRegisterOrdersSameDayByID pins the tie-break, so a running balance does
// not reshuffle between page loads.
func TestRegisterOrdersSameDayByID(t *testing.T) {
	store, acct := checking(t, "0.00")

	for _, payee := range []string{"first", "second", "third"} {
		if _, err := transaction.Create(t.Context(), store, acct, transaction.New{
			Date: "2026-08-14", Payee: payee, Amount: usd(t, "-1.00"),
		}); err != nil {
			t.Fatalf("Create(%q): %v", payee, err)
		}
	}

	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	for i, want := range []string{"first", "second", "third"} {
		if got := reg.Entries[i].Payee; got != want {
			t.Errorf("entry %d payee = %q, want %q", i, got, want)
		}
	}
}

// TestRegisterCategories checks what the category column is told: one split
// names its category, several name none, and none at all is not an error.
func TestRegisterCategories(t *testing.T) {
	store, acct := checking(t, "0.00")

	groceries, err := category.Ensure(t.Context(), store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	household, err := category.Ensure(t.Context(), store, "Household")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	create := func(n transaction.New) {
		t.Helper()
		if _, err := transaction.Create(t.Context(), store, acct, n); err != nil {
			t.Fatalf("Create(%q): %v", n.Payee, err)
		}
	}
	create(transaction.New{
		Date: "2026-08-01", Payee: "one split", Amount: usd(t, "-10.00"),
		Splits: []transaction.Split{{CategoryID: groceries.ID, Amount: usd(t, "-10.00")}},
	})
	create(transaction.New{
		Date: "2026-08-02", Payee: "two splits", Amount: usd(t, "-30.00"),
		Splits: []transaction.Split{
			{CategoryID: groceries.ID, Amount: usd(t, "-20.00")},
			{CategoryID: household.ID, Amount: usd(t, "-10.00")},
		},
	})
	create(transaction.New{Date: "2026-08-03", Payee: "no splits", Amount: usd(t, "-5.00")})
	create(transaction.New{
		Date: "2026-08-04", Payee: "uncategorized split", Amount: usd(t, "-5.00"),
		Splits: []transaction.Split{{Amount: usd(t, "-5.00")}},
	})

	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}

	want := []struct {
		payee    string
		category string
		splits   int
		isSplit  bool
	}{
		{"one split", "Groceries", 1, false},
		{"two splits", "", 2, true},
		{"no splits", "", 0, false},
		{"uncategorized split", "", 1, false},
	}
	if len(reg.Entries) != len(want) {
		t.Fatalf("register has %d entries, want %d", len(reg.Entries), len(want))
	}
	for i, w := range want {
		e := reg.Entries[i]
		if e.Payee != w.payee {
			t.Fatalf("entry %d payee = %q, want %q", i, e.Payee, w.payee)
		}
		if e.SplitCount != w.splits {
			t.Errorf("%s: split count = %d, want %d", w.payee, e.SplitCount, w.splits)
		}
		if e.IsSplit() != w.isSplit {
			t.Errorf("%s: IsSplit = %v, want %v", w.payee, e.IsSplit(), w.isSplit)
		}
		// A transaction divided among categories names none of them: showing
		// the first would claim the whole amount went there.
		if !w.isSplit && e.Category != w.category {
			t.Errorf("%s: category = %q, want %q", w.payee, e.Category, w.category)
		}
	}
}

func TestRegisterEmptyAccount(t *testing.T) {
	store, acct := checking(t, "250.00")

	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("register has %d entries, want 0", len(reg.Entries))
	}
	// An account with no transactions still has its opening balance.
	if got := reg.Ending.Decimal(); got != "250.00" {
		t.Errorf("ending balance = %s, want 250.00", got)
	}
	if got := reg.Cleared.Decimal(); got != "250.00" {
		t.Errorf("cleared balance = %s, want 250.00", got)
	}
}

// TestRegisterIgnoresOtherAccounts guards against a missing WHERE clause, which
// would show one account's balance built from another's rows.
func TestRegisterIgnoresOtherAccounts(t *testing.T) {
	store, checkingAcct := checking(t, "0.00")

	savings, err := account.Create(t.Context(), store, account.New{
		Name: "Savings", Type: account.Savings, Currency: money.USD,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := transaction.Create(t.Context(), store, savings, transaction.New{
		Date: "2026-08-01", Payee: "interest", Amount: usd(t, "1.23"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	reg, err := transaction.LoadRegister(t.Context(), store, checkingAcct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(reg.Entries) != 0 {
		t.Fatalf("checking register has %d entries, want 0", len(reg.Entries))
	}
}

func TestCreateRejects(t *testing.T) {
	tests := []struct {
		name string
		in   transaction.New
		want error
	}{
		{
			"malformed date",
			transaction.New{Date: "08/14/2026", Payee: "x"},
			transaction.ErrInvalidDate,
		},
		{
			// The schema's GLOB only checks the shape; a day that does not
			// exist is caught here.
			"impossible date",
			transaction.New{Date: "2026-02-31", Payee: "x"},
			transaction.ErrInvalidDate,
		},
		{
			"unknown status",
			transaction.New{Date: "2026-08-14", Payee: "x", Status: "pending"},
			transaction.ErrInvalidStatus,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, acct := checking(t, "0.00")
			tt.in.Amount = usd(t, "-1.00")
			if _, err := transaction.Create(t.Context(), store, acct, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("Create = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCreateRejectsForeignCurrency(t *testing.T) {
	store, acct := checking(t, "0.00")

	eur, err := money.ParseDecimal("-10.00", money.EUR)
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}
	_, err = transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-08-14", Payee: "x", Amount: eur,
	})
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Create = %v, want ErrCurrencyMismatch", err)
	}
}

// TestCreateRejectsUnbalancedSplits keeps a transaction from being categorized
// into something other than its own amount, which would make category totals
// disagree with the register.
func TestCreateRejectsUnbalancedSplits(t *testing.T) {
	store, acct := checking(t, "0.00")

	_, err := transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Splits: []transaction.Split{
			{Amount: usd(t, "-71.22")},
			{Amount: usd(t, "-10.00")},
		},
	})
	if !errors.Is(err, transaction.ErrSplitTotal) {
		t.Fatalf("Create = %v, want ErrSplitTotal", err)
	}

	// Nothing may be left behind by a rejected write.
	if got := countRows(t, store, "transactions"); got != 0 {
		t.Errorf("%d transactions written, want 0", got)
	}
	if got := countRows(t, store, "splits"); got != 0 {
		t.Errorf("%d splits written, want 0", got)
	}
}

// TestCreateRollsBackOnSplitFailure checks the transaction boundary: a split
// that cannot be written must take the whole entry with it rather than leaving a
// half-categorized row in the register.
func TestCreateRollsBackOnSplitFailure(t *testing.T) {
	store, acct := checking(t, "0.00")

	// Category 999 does not exist, so the foreign key rejects the second split.
	// Foreign keys are enforced on every connection the pool hands out, which is
	// what makes this fail rather than silently store a dangling reference.
	_, err := transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-30.00"),
		Splits: []transaction.Split{
			{Amount: usd(t, "-20.00")},
			{CategoryID: 999, Amount: usd(t, "-10.00")},
		},
	})
	if err == nil {
		t.Fatal("Create succeeded with a split naming a category that does not exist")
	}

	if got := countRows(t, store, "transactions"); got != 0 {
		t.Errorf("%d transactions left behind, want 0", got)
	}
	if got := countRows(t, store, "splits"); got != 0 {
		t.Errorf("%d splits left behind, want 0", got)
	}
}

func TestStatusHasCleared(t *testing.T) {
	for _, tt := range []struct {
		status transaction.Status
		want   bool
	}{
		{transaction.Uncleared, false},
		{transaction.Cleared, true},
		{transaction.Reconciled, true},
	} {
		if got := tt.status.HasCleared(); got != tt.want {
			t.Errorf("%s.HasCleared() = %v, want %v", tt.status, got, tt.want)
		}
	}
}

// countRows returns the number of rows in a table.
func countRows(t *testing.T, store *storage.Store, table string) int64 {
	t.Helper()
	conn, err := store.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer store.Put(conn)

	var n int64
	if err := sqlitex.ExecuteTransient(conn, `SELECT COUNT(*) FROM `+table+`;`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			n = stmt.ColumnInt64(0)
			return nil
		},
	}); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
