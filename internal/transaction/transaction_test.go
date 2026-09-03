// Copyright (c) 2026 Michael D Henderson.

package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
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

// TestSetStatusMarksCleared is the register's toggle: the bank has shown the
// transaction, and nothing else about it changes.
func TestSetStatusMarksCleared(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if txn.Status != transaction.Uncleared {
		t.Fatalf("status = %q, want uncleared", txn.Status)
	}

	got, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Cleared, txn.UpdatedAt)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got.Status != transaction.Cleared {
		t.Errorf("status = %q, want cleared", got.Status)
	}
	if !got.UpdatedAt.After(txn.UpdatedAt) {
		t.Errorf("updated_at did not advance: %s then %s", txn.UpdatedAt, got.UpdatedAt)
	}
	if got.Amount.Decimal() != "-84.17" || got.Payee != "Riba Smith" || got.Date != "2026-08-14" {
		t.Errorf("marking cleared changed something else: %+v", got)
	}

	// And back again: the mark is a toggle, not a one-way door.
	back, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Uncleared, got.UpdatedAt)
	if err != nil {
		t.Fatalf("SetStatus back: %v", err)
	}
	if back.Status != transaction.Uncleared {
		t.Errorf("status = %q, want uncleared", back.Status)
	}
}

// TestSetStatusRefusesStaleToken is CO-3 itself: two tabs, and the second write
// is told rather than allowed to overwrite.
func TestSetStatusRefusesStaleToken(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := txn.UpdatedAt // what both tabs read

	if _, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Cleared, stale); err != nil {
		t.Fatalf("first SetStatus: %v", err)
	}

	_, err = transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Uncleared, stale)
	if !errors.Is(err, transaction.ErrConflict) {
		t.Fatalf("second SetStatus err = %v, want transaction.ErrConflict", err)
	}

	// The first write stands. A refused write changes nothing.
	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if reg.Entries[0].Status != transaction.Cleared {
		t.Errorf("status = %q, want cleared: the refused write was applied anyway", reg.Entries[0].Status)
	}
}

// TestSetStatusRefusesReconciled: a finished reconciliation is a fact, and the
// register does not rewrite it (RC-3).
func TestSetStatusRefusesReconciled(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Acme Manufacturing", Amount: usd(t, "2480.16"), Status: transaction.Reconciled,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Uncleared, txn.UpdatedAt); !errors.Is(err, transaction.ErrReconciled) {
		t.Fatalf("err = %v, want transaction.ErrReconciled", err)
	}
}

// TestSetStatusRefusesReconciling: reconciled is recorded by a reconciliation,
// never by the register's toggle.
func TestSetStatusRefusesReconciling(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Reconciled, txn.UpdatedAt); !errors.Is(err, transaction.ErrInvalidStatus) {
		t.Fatalf("err = %v, want transaction.ErrInvalidStatus", err)
	}
}

// TestSetStatusUnchangedKeepsToken: setting the status it already has is not a
// write. Burning a token would make every other open tab stale for no change.
func TestSetStatusUnchangedKeepsToken(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Uncleared, txn.UpdatedAt)
	if err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if !got.UpdatedAt.Equal(txn.UpdatedAt) {
		t.Errorf("updated_at moved on a no-op write: %s then %s", txn.UpdatedAt, got.UpdatedAt)
	}
}

// TestSetStatusOtherAccount: the id comes from a URL, and a URL can be edited.
func TestSetStatusOtherAccount(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	other, err := account.Create(ctx, store, account.New{Name: "Savings", Type: account.Savings, Currency: money.USD})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17")})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := transaction.SetStatus(ctx, store, other, txn.ID, transaction.Cleared, txn.UpdatedAt); !errors.Is(err, transaction.ErrNotFound) {
		t.Fatalf("err = %v, want transaction.ErrNotFound", err)
	}
}

// splitsOf reads a transaction's splits as (category id, decimal amount) pairs,
// through SQL rather than through the package, so a test can tell what was
// actually written from what the package says it wrote.
func splitsOf(t *testing.T, store *storage.Store, txnID int64) [][2]string {
	t.Helper()
	conn, err := store.Conn(context.Background())
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer store.Put(conn)

	var got [][2]string
	if err := sqlitex.ExecuteTransient(conn, `
SELECT COALESCE(category_id, 0), amount FROM splits WHERE transaction_id = ? ORDER BY id;`,
		&sqlitex.ExecOptions{
			Args: []any{txnID},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				amount, err := money.NewMinor(stmt.ColumnInt64(1), money.USD)
				if err != nil {
					return err
				}
				got = append(got, [2]string{strconv.FormatInt(stmt.ColumnInt64(0), 10), amount.Decimal()})
				return nil
			},
		}); err != nil {
		t.Fatalf("read splits: %v", err)
	}
	return got
}

// edited returns an Edit that would leave txn exactly as it is, so a test can
// change one field and be sure it changed only that one.
func edited(txn transaction.Transaction) transaction.Edit {
	return transaction.Edit{
		Date:        txn.Date,
		Payee:       txn.Payee,
		Memo:        txn.Memo,
		Amount:      txn.Amount,
		CheckNumber: txn.CheckNumber,
	}
}

// TestUpdateChangesEveryField is RG-2's "change": everything typed on the entry
// form can be typed again.
func TestUpdateChangesEveryField(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "100.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smth", Memo: "grocerys",
		Amount: usd(t, "-84.17"), CheckNumber: "101",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := transaction.Update(ctx, store, acct, txn.ID, transaction.Edit{
		Date: "2026-08-15", Payee: "Riba Smith", Memo: "groceries",
		Amount: usd(t, "-84.71"), CheckNumber: "102",
	}, txn.UpdatedAt)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	if got.Date != "2026-08-15" || got.Payee != "Riba Smith" || got.Memo != "groceries" ||
		got.Amount.Decimal() != "-84.71" || got.CheckNumber != "102" {
		t.Errorf("Update returned %+v", got)
	}
	if !got.UpdatedAt.After(txn.UpdatedAt) {
		t.Errorf("updated_at did not advance: %s then %s", txn.UpdatedAt, got.UpdatedAt)
	}
	// Status is not an editable field: clearing is a separate fact (RC-3).
	if got.Status != transaction.Uncleared {
		t.Errorf("status = %q, want uncleared", got.Status)
	}

	// And the register agrees, which is what the household actually sees.
	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if reg.Entries[0].Payee != "Riba Smith" || reg.Ending.Decimal() != "15.29" {
		t.Errorf("register shows %q at %s, want Riba Smith at 15.29",
			reg.Entries[0].Payee, reg.Ending.Decimal())
	}
}

// TestUpdateRefusesStaleToken is CO-3 on an edit: the second tab is told, and
// the first tab's work stands.
func TestUpdateRefusesStaleToken(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := txn.UpdatedAt // what both tabs read

	first := edited(txn)
	first.Payee = "Riba Smith SA"
	if _, err := transaction.Update(ctx, store, acct, txn.ID, first, stale); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	second := edited(txn)
	second.Payee = "Super 99"
	_, err = transaction.Update(ctx, store, acct, txn.ID, second, stale)
	if !errors.Is(err, transaction.ErrConflict) {
		t.Fatalf("second Update err = %v, want transaction.ErrConflict", err)
	}

	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	// Not a mixture of the two: the refused write left nothing behind.
	if reg.Entries[0].Payee != "Riba Smith SA" {
		t.Errorf("payee = %q, want Riba Smith SA", reg.Entries[0].Payee)
	}
}

// TestUpdateReplacesSplits: the splits are the new set, not the new set added to
// the old one.
func TestUpdateReplacesSplits(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	groceries, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	household, err := category.Ensure(ctx, store, "Household")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Costco", Amount: usd(t, "-150.00"),
		Splits: []transaction.Split{{CategoryID: groceries.ID, Amount: usd(t, "-150.00")}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn)
	e.Splits = &[]transaction.Split{
		{CategoryID: groceries.ID, Amount: usd(t, "-90.00")},
		{CategoryID: household.ID, Amount: usd(t, "-60.00")},
	}
	got, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := [][2]string{
		{strconv.FormatInt(groceries.ID, 10), "-90.00"},
		{strconv.FormatInt(household.ID, 10), "-60.00"},
	}
	if diff := splitsOf(t, store, txn.ID); !reflect.DeepEqual(diff, want) {
		t.Errorf("splits = %v, want %v", diff, want)
	}
	if n := countRows(t, store, "splits"); n != 2 {
		t.Errorf("splits table holds %d rows, want 2: the old ones were not removed", n)
	}

	// Edit again, back to one, so the replacement is not just an append that
	// happened to look right the first time.
	e2 := edited(got)
	e2.Splits = &[]transaction.Split{{CategoryID: household.ID, Amount: usd(t, "-150.00")}}
	if _, err := transaction.Update(ctx, store, acct, txn.ID, e2, got.UpdatedAt); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if n := countRows(t, store, "splits"); n != 1 {
		t.Errorf("splits table holds %d rows, want 1", n)
	}
}

// TestUpdateRemovesSplits: an empty set is not a nil one. Clearing the category
// leaves an uncategorized transaction, which is a normal state.
func TestUpdateRemovesSplits(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	cat, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Splits: []transaction.Split{{CategoryID: cat.ID, Amount: usd(t, "-84.17")}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn)
	e.Splits = &[]transaction.Split{}
	if _, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt); err != nil {
		t.Fatalf("Update: %v", err)
	}

	if n := countRows(t, store, "splits"); n != 0 {
		t.Errorf("splits table holds %d rows, want 0", n)
	}
	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if reg.Entries[0].Category != "" || reg.Entries[0].SplitCount != 0 {
		t.Errorf("entry is %q with %d splits, want uncategorized",
			reg.Entries[0].Category, reg.Entries[0].SplitCount)
	}
}

// TestUpdateKeepsSplitsWhenNotGiven: a caller that does not understand a
// transaction's categories can still change its payee without flattening them.
func TestUpdateKeepsSplitsWhenNotGiven(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	groceries, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	household, err := category.Ensure(ctx, store, "Household")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Costcp", Amount: usd(t, "-150.00"),
		Splits: []transaction.Split{
			{CategoryID: groceries.ID, Amount: usd(t, "-90.00")},
			{CategoryID: household.ID, Amount: usd(t, "-60.00")},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn) // Splits nil: leave them alone
	e.Payee = "Costco"
	if _, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt); err != nil {
		t.Fatalf("Update: %v", err)
	}

	want := [][2]string{
		{strconv.FormatInt(groceries.ID, 10), "-90.00"},
		{strconv.FormatInt(household.ID, 10), "-60.00"},
	}
	if got := splitsOf(t, store, txn.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("splits = %v, want %v: leaving them alone rewrote them", got, want)
	}
}

// TestUpdateRefusesUnbalancedSplits, both ways round: a new set that does not
// total, and a new amount that no longer matches the set left in place. Neither
// writes anything.
func TestUpdateRefusesUnbalancedSplits(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	cat, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Splits: []transaction.Split{{CategoryID: cat.ID, Amount: usd(t, "-84.17")}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("a set that does not total", func(t *testing.T) {
		e := edited(txn)
		e.Splits = &[]transaction.Split{{CategoryID: cat.ID, Amount: usd(t, "-80.00")}}
		if _, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt); !errors.Is(err, transaction.ErrSplitTotal) {
			t.Fatalf("Update err = %v, want transaction.ErrSplitTotal", err)
		}
	})

	t.Run("an amount the stored set no longer matches", func(t *testing.T) {
		e := edited(txn) // Splits nil
		e.Amount = usd(t, "-90.00")
		if _, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt); !errors.Is(err, transaction.ErrSplitTotal) {
			t.Fatalf("Update err = %v, want transaction.ErrSplitTotal", err)
		}
	})

	// Nothing was written by either, which is what the single immediate
	// transaction buys: the parent row is not corrected while the splits are not.
	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if reg.Entries[0].Amount.Decimal() != "-84.17" {
		t.Errorf("amount = %s, want -84.17", reg.Entries[0].Amount.Decimal())
	}
	if got := splitsOf(t, store, txn.ID); len(got) != 1 || got[0][1] != "-84.17" {
		t.Errorf("splits = %v, want the original one", got)
	}
}

// TestUpdateUnchangedKeepsToken: saving a form nobody changed is not a change,
// and must not make every other tab on this register stale.
func TestUpdateUnchangedKeepsToken(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	cat, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Splits: []transaction.Split{{CategoryID: cat.ID, Amount: usd(t, "-84.17")}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn)
	e.Splits = &[]transaction.Split{{CategoryID: cat.ID, Amount: usd(t, "-84.17")}}
	got, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !got.UpdatedAt.Equal(txn.UpdatedAt) {
		t.Errorf("updated_at moved from %s to %s for an edit that changed nothing",
			txn.UpdatedAt, got.UpdatedAt)
	}
}

// TestUpdateRefusesReconciled: RC-3 on every field, not only on the status.
func TestUpdateRefusesReconciled(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Status: transaction.Reconciled,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn)
	e.Memo = "a note that changes no balance"
	if _, err := transaction.Update(ctx, store, acct, txn.ID, e, txn.UpdatedAt); !errors.Is(err, transaction.ErrReconciled) {
		t.Fatalf("Update err = %v, want transaction.ErrReconciled", err)
	}
	if err := transaction.Delete(ctx, store, acct, txn.ID, txn.UpdatedAt); !errors.Is(err, transaction.ErrReconciled) {
		t.Fatalf("Delete err = %v, want transaction.ErrReconciled", err)
	}
	if n := countRows(t, store, "transactions"); n != 1 {
		t.Errorf("transactions table holds %d rows, want 1", n)
	}
}

// TestUpdateRejects covers the checks made before anything is read.
func TestUpdateRejects(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	eur, err := money.ParseDecimal("-84.17", money.EUR)
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}

	for _, tc := range []struct {
		name string
		edit func(transaction.Edit) transaction.Edit
		want error
	}{
		{"not a date", func(e transaction.Edit) transaction.Edit { e.Date = "2026-02-30"; return e }, transaction.ErrInvalidDate},
		{"not a date at all", func(e transaction.Edit) transaction.Edit { e.Date = "last Tuesday"; return e }, transaction.ErrInvalidDate},
		{"another currency", func(e transaction.Edit) transaction.Edit { e.Amount = eur; return e }, money.ErrCurrencyMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := transaction.Update(ctx, store, acct, txn.ID, tc.edit(edited(txn)), txn.UpdatedAt); !errors.Is(err, tc.want) {
				t.Fatalf("Update err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestUpdateOtherAccount: the id comes from a URL, and a URL can be edited. One
// account's transaction is not reachable through another's register.
func TestUpdateOtherAccount(t *testing.T) {
	ctx := t.Context()
	store, checkingAcct := checking(t, "0.00")

	savings, err := account.Create(ctx, store, account.New{
		Name: "Savings", Type: account.Savings, Currency: money.USD, OpeningBalance: usd(t, "0.00"),
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	txn, err := transaction.Create(ctx, store, checkingAcct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := edited(txn)
	e.Payee = "somebody else's register"
	if _, err := transaction.Update(ctx, store, savings, txn.ID, e, txn.UpdatedAt); !errors.Is(err, transaction.ErrNotFound) {
		t.Fatalf("Update err = %v, want transaction.ErrNotFound", err)
	}
	if err := transaction.Delete(ctx, store, savings, txn.ID, txn.UpdatedAt); !errors.Is(err, transaction.ErrNotFound) {
		t.Fatalf("Delete err = %v, want transaction.ErrNotFound", err)
	}
}

// TestDeleteRemovesTransactionAndSplits is RG-2's "remove", and the cascade that
// goes with it. The splits go because foreign_keys is on, not because a second
// statement remembered them.
func TestDeleteRemovesTransactionAndSplits(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "100.00")

	cat, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
		Splits: []transaction.Split{
			{CategoryID: cat.ID, Amount: usd(t, "-40.00")},
			{CategoryID: cat.ID, Amount: usd(t, "-44.17")},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	keep, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-15", Payee: "Felipe Motta", Amount: usd(t, "-10.00"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := transaction.Delete(ctx, store, acct, txn.ID, txn.UpdatedAt); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := countRows(t, store, "splits"); n != 0 {
		t.Errorf("splits table holds %d rows, want 0: the cascade did not run", n)
	}
	reg, err := transaction.LoadRegister(ctx, store, acct)
	if err != nil {
		t.Fatalf("LoadRegister: %v", err)
	}
	if len(reg.Entries) != 1 || reg.Entries[0].ID != keep.ID {
		t.Fatalf("register holds %d entries, want only the one that was kept", len(reg.Entries))
	}
	// The balance is the point: a removed transaction leaves the running total.
	if reg.Ending.Decimal() != "90.00" {
		t.Errorf("ending balance = %s, want 90.00", reg.Ending.Decimal())
	}
	if reg.Entries[0].Balance.Decimal() != "90.00" {
		t.Errorf("running balance = %s, want 90.00", reg.Entries[0].Balance.Decimal())
	}
}

// TestDeleteRefusesStaleToken: removing is a write, and a write from a tab that
// has gone stale is refused like any other (CO-3).
func TestDeleteRefusesStaleToken(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := txn.UpdatedAt

	if _, err := transaction.SetStatus(ctx, store, acct, txn.ID, transaction.Cleared, stale); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if err := transaction.Delete(ctx, store, acct, txn.ID, stale); !errors.Is(err, transaction.ErrConflict) {
		t.Fatalf("Delete err = %v, want transaction.ErrConflict", err)
	}
	if n := countRows(t, store, "transactions"); n != 1 {
		t.Errorf("transactions table holds %d rows, want 1: the refused delete ran anyway", n)
	}
}

// TestDeleteMissing: an id that never existed, or one already removed.
func TestDeleteMissing(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	txn, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Amount: usd(t, "-84.17"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := transaction.Delete(ctx, store, acct, txn.ID, txn.UpdatedAt); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Twice. Ids are never reused (ST-9), so the second press cannot land on
	// somebody else's transaction; it lands on nothing.
	if err := transaction.Delete(ctx, store, acct, txn.ID, txn.UpdatedAt); !errors.Is(err, transaction.ErrNotFound) {
		t.Fatalf("second Delete err = %v, want transaction.ErrNotFound", err)
	}
}

// TestGetReadsOneTransaction: the edit form is built from this, so it has to
// carry the category and the split count the register shows.
func TestGetReadsOneTransaction(t *testing.T) {
	ctx := t.Context()
	store, acct := checking(t, "0.00")

	groceries, err := category.Ensure(ctx, store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	household, err := category.Ensure(ctx, store, "Household")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	plain, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-14", Payee: "Riba Smith", Memo: "weekly", Amount: usd(t, "-84.17"),
		CheckNumber: "101",
		Splits:      []transaction.Split{{CategoryID: groceries.ID, Amount: usd(t, "-84.17")}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	split, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-15", Payee: "Costco", Amount: usd(t, "-150.00"),
		Splits: []transaction.Split{
			{CategoryID: groceries.ID, Amount: usd(t, "-90.00")},
			{CategoryID: household.ID, Amount: usd(t, "-60.00")},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	bare, err := transaction.Create(ctx, store, acct, transaction.New{
		Date: "2026-08-16", Payee: "Panama Joe", Amount: usd(t, "-18.42"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := transaction.Get(ctx, store, acct, plain.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Payee != "Riba Smith" || got.Memo != "weekly" || got.CheckNumber != "101" ||
		got.Amount.Decimal() != "-84.17" || got.SplitCount() != 1 || got.IsSplit() {
		t.Errorf("Get returned %+v", got)
	}
	if got.Splits[0].Category != "Groceries" || got.Splits[0].Amount.Decimal() != "-84.17" {
		t.Errorf("the one part is %+v, want all of it in Groceries", got.Splits[0])
	}
	if !got.UpdatedAt.Equal(plain.UpdatedAt) {
		t.Errorf("token = %s, want %s: the form would send back a stale one", got.UpdatedAt, plain.UpdatedAt)
	}

	// A split editor is built from the parts themselves, in id order, with the
	// names a form fills its boxes with.
	if got, err := transaction.Get(ctx, store, acct, split.ID); err != nil {
		t.Fatalf("Get split: %v", err)
	} else if got.SplitCount() != 2 || !got.IsSplit() {
		t.Errorf("split has %d splits, IsSplit = %v", got.SplitCount(), got.IsSplit())
	} else if got.Splits[0].Category != "Groceries" || got.Splits[0].Amount.Decimal() != "-90.00" ||
		got.Splits[1].Category != "Household" || got.Splits[1].Amount.Decimal() != "-60.00" {
		t.Errorf("the parts read back as %+v", got.Splits)
	}

	if got, err := transaction.Get(ctx, store, acct, bare.ID); err != nil {
		t.Fatalf("Get uncategorized: %v", err)
	} else if got.SplitCount() != 0 || len(got.Splits) != 0 {
		t.Errorf("uncategorized transaction has %d splits", got.SplitCount())
	}

	if _, err := transaction.Get(ctx, store, acct, plain.ID+9999); !errors.Is(err, transaction.ErrNotFound) {
		t.Fatalf("Get missing err = %v, want transaction.ErrNotFound", err)
	}
}
