// Copyright (c) 2026 Michael D Henderson.

// Package transaction reads and writes register entries.
//
// It also assembles the register itself: the rows of an account in order, each
// carrying its category and the running balance through it (SPECIFICATION.md
// RG-1). The running balance is computed here, in the domain, rather than in a
// template, so the terminal register and any export agree with the browser
// (TS-2, TS-3).
package transaction

import (
	"context"
	"fmt"
	"time"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

const (
	// ErrNotFound is returned when no transaction has the requested id.
	ErrNotFound = cerrs.Error("transaction not found")

	// ErrInvalidDate is returned when a date is not a real calendar date in
	// YYYY-MM-DD form.
	ErrInvalidDate = cerrs.Error("invalid date")

	// ErrInvalidStatus is returned when a status is not one the schema accepts.
	ErrInvalidStatus = cerrs.Error("invalid status")

	// ErrSplitTotal is returned when the splits of a transaction do not add up
	// to its amount.
	ErrSplitTotal = cerrs.Error("splits do not total the transaction amount")
)

// DateLayout is the storage and display form of a calendar date.
//
// A calendar date is not an instant: it carries no timezone and is never
// converted to one (SPECIFICATION.md ST-8). The layout is ISO 8601 so that the
// TEXT column sorts chronologically.
const DateLayout = "2006-01-02"

// Status is where a transaction stands against the bank.
//
// Cleared means the household saw it on the bank's side; reconciled means a
// finished reconciliation recorded it (SPECIFICATION.md RC-3). They are
// different facts and the register does not collapse them.
type Status string

const (
	Uncleared  Status = "uncleared"
	Cleared    Status = "cleared"
	Reconciled Status = "reconciled"
)

// Valid reports whether s is a status the schema accepts.
func (s Status) Valid() bool {
	switch s {
	case Uncleared, Cleared, Reconciled:
		return true
	}
	return false
}

// HasCleared reports whether the bank has seen this transaction, which is what
// the cleared balance sums over.
func (s Status) HasCleared() bool { return s == Cleared || s == Reconciled }

// Transaction is one entry in an account's register.
type Transaction struct {
	ID        int64
	AccountID int64

	// Date is a calendar date, "YYYY-MM-DD" (ST-8).
	Date string

	Payee       string
	Memo        string
	Amount      money.Money
	Status      Status
	CheckNumber string

	CreatedAt time.Time

	// UpdatedAt is also the optimistic concurrency token (CO-3): an edit sends
	// back the value it read, and a write from a tab holding a stale value
	// matches no rows instead of overwriting someone else's change.
	UpdatedAt time.Time
}

// Entry is a register row: a transaction, the category to show for it, and the
// balance through it.
type Entry struct {
	Transaction

	// Category is the name to display. It is empty when the transaction has no
	// splits or its one split has no category; the UI decides what to call that.
	Category string

	// SplitCount is how many splits the transaction has. More than one means
	// Category is only the first of several, and the row should say so rather
	// than pretend the whole amount went to it.
	SplitCount int

	// Balance is the account balance through this row, inclusive.
	Balance money.Money
}

// IsSplit reports whether the entry is divided among more than one category.
func (e Entry) IsSplit() bool { return e.SplitCount > 1 }

// Register is an account's rows with the totals that explain them.
type Register struct {
	Account account.Account

	// Entries are ordered by date, then by id. Ids are never reused and always
	// increase (ST-9), so two transactions on the same day stay in the order
	// they were entered, and the running balance is reproducible.
	Entries []Entry

	// Ending is the balance after every entry: the opening balance plus every
	// amount.
	Ending money.Money

	// Cleared is the opening balance plus the amounts the bank has seen. The
	// difference between it and Ending is what has not reached the bank yet, so
	// the two are shown together rather than reconciled into one number (RC-2).
	Cleared money.Money

	// UnclearedCount is how many entries are still uncleared.
	UnclearedCount int
}

// selectEntry lists the register columns. The two correlated subqueries are what
// give a row its category without a GROUP BY that would have to be undone to get
// the split count back.
const selectEntry = `
SELECT t.id, t.account_id, t.date, t.payee, t.memo, t.amount, t.status,
       t.check_number, t.created_at, t.updated_at,
       (SELECT COUNT(*) FROM splits s WHERE s.transaction_id = t.id) AS split_count,
       (SELECT COALESCE(c.name, '')
          FROM splits s
          LEFT JOIN categories c ON c.id = s.category_id
         WHERE s.transaction_id = t.id
         ORDER BY s.id
         LIMIT 1) AS category
  FROM transactions t`

// LoadRegister returns acct's register, in order, with running balances.
//
// The whole register is read at once. A household's account holds a few thousand
// rows at most, and paging would mean either a running balance that restarts on
// every page or a second query to compute the offset -- neither of which is
// worth the trouble at this size (TS-4).
func LoadRegister(ctx context.Context, store *storage.Store, acct account.Account) (Register, error) {
	reg := Register{
		Account: acct,
		Ending:  acct.OpeningBalance,
		Cleared: acct.OpeningBalance,
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Register{}, fmt.Errorf("load register for %q: %w", acct.Name, err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(selectEntry + `
 WHERE t.account_id = $account_id
 ORDER BY t.date, t.id;`)
	if err != nil {
		return Register{}, fmt.Errorf("load register for %q: %w", acct.Name, err)
	}
	defer stmt.Reset()
	stmt.SetInt64("$account_id", acct.ID)

	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return Register{}, fmt.Errorf("load register for %q: %w", acct.Name, err)
		}
		if !hasRow {
			return reg, nil
		}

		e, err := scanEntry(stmt, acct.Currency)
		if err != nil {
			return Register{}, fmt.Errorf("load register for %q: %w", acct.Name, err)
		}

		if reg.Ending, err = reg.Ending.Add(e.Amount); err != nil {
			return Register{}, fmt.Errorf("load register for %q: transaction %d: %w", acct.Name, e.ID, err)
		}
		if e.Status.HasCleared() {
			if reg.Cleared, err = reg.Cleared.Add(e.Amount); err != nil {
				return Register{}, fmt.Errorf("load register for %q: transaction %d: %w", acct.Name, e.ID, err)
			}
		} else {
			reg.UnclearedCount++
		}

		e.Balance = reg.Ending
		reg.Entries = append(reg.Entries, e)
	}
}

// Split is one part of a transaction's amount, assigned to a category.
type Split struct {
	// CategoryID is zero for a split that has not been categorized, which the
	// schema stores as NULL.
	CategoryID int64
	Amount     money.Money
	Memo       string
}

// New describes a transaction to create.
type New struct {
	Date        string
	Payee       string
	Memo        string
	Amount      money.Money
	Status      Status
	CheckNumber string

	// Splits divide Amount among categories. They must total Amount exactly. A
	// transaction with no splits is uncategorized, which is a normal state and
	// not an error.
	Splits []Split
}

// Create inserts a transaction, with its splits, into acct.
//
// The transaction and its splits are written in one database transaction, so a
// failure part way through leaves no half-categorized entry behind.
func Create(ctx context.Context, store *storage.Store, acct account.Account, n New) (Transaction, error) {
	if _, err := time.Parse(DateLayout, n.Date); err != nil {
		return Transaction{}, fmt.Errorf("%q: %w", n.Date, ErrInvalidDate)
	}
	if n.Status == "" {
		n.Status = Uncleared
	}
	if !n.Status.Valid() {
		return Transaction{}, fmt.Errorf("%q: %w", n.Status, ErrInvalidStatus)
	}
	if n.Amount.Currency() != acct.Currency {
		return Transaction{}, fmt.Errorf("amount is %s, account %q is %s: %w",
			n.Amount.Currency(), acct.Name, acct.Currency, money.ErrCurrencyMismatch)
	}
	if err := checkSplitTotal(n.Amount, n.Splits); err != nil {
		return Transaction{}, err
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	defer store.Put(conn)

	return insert(conn, acct, n)
}

// insert writes the transaction and its splits inside one database transaction.
// It is separate from Create so the deferred rollback can see a named error.
func insert(conn *sqlite.Conn, acct account.Account, n New) (txn Transaction, err error) {
	// IMMEDIATE takes the write lock now rather than partway through, so a
	// second writer fails to begin instead of failing to upgrade after the
	// splits are already written.
	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	defer endTx(&err)

	stmt, err := conn.Prepare(`
INSERT INTO transactions (account_id, date, payee, memo, amount, status, check_number)
VALUES ($account_id, $date, $payee, $memo, $amount, $status, $check_number)
RETURNING id, account_id, date, payee, memo, amount, status, check_number,
          created_at, updated_at;`)
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	stmt.SetInt64("$account_id", acct.ID)
	stmt.SetText("$date", n.Date)
	stmt.SetText("$payee", n.Payee)
	stmt.SetText("$memo", n.Memo)
	storage.BindMoney(stmt, "$amount", n.Amount)
	stmt.SetText("$status", string(n.Status))
	stmt.SetText("$check_number", n.CheckNumber)

	hasRow, err := stmt.Step()
	if err != nil {
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}
	if !hasRow {
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("create transaction: insert returned no row")
	}
	txn, err = scanTransaction(stmt, acct.Currency)
	if resetErr := stmt.Reset(); err == nil {
		err = resetErr
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("create transaction: %w", err)
	}

	for i, s := range n.Splits {
		split, err := conn.Prepare(`
INSERT INTO splits (transaction_id, category_id, amount, memo)
VALUES ($transaction_id, $category_id, $amount, $memo);`)
		if err != nil {
			return Transaction{}, fmt.Errorf("create transaction: split %d: %w", i+1, err)
		}
		split.SetInt64("$transaction_id", txn.ID)
		if s.CategoryID == 0 {
			split.SetNull("$category_id")
		} else {
			split.SetInt64("$category_id", s.CategoryID)
		}
		storage.BindMoney(split, "$amount", s.Amount)
		split.SetText("$memo", s.Memo)

		_, err = split.Step()
		if resetErr := split.Reset(); err == nil {
			err = resetErr
		}
		if err != nil {
			return Transaction{}, fmt.Errorf("create transaction: split %d: %w", i+1, err)
		}
	}

	return txn, nil
}

// checkSplitTotal reports whether the splits add up to amount. A transaction with
// no splits is uncategorized, not unbalanced.
func checkSplitTotal(amount money.Money, splits []Split) error {
	if len(splits) == 0 {
		return nil
	}
	total, err := money.Zero(amount.Currency())
	if err != nil {
		return fmt.Errorf("total splits: %w", err)
	}
	for i, s := range splits {
		if total, err = total.Add(s.Amount); err != nil {
			return fmt.Errorf("total splits: split %d: %w", i+1, err)
		}
	}
	same, err := total.Equals(amount)
	if err != nil {
		return fmt.Errorf("total splits: %w", err)
	}
	if !same {
		return fmt.Errorf("splits total %s, transaction is %s: %w",
			total.Decimal(), amount.Decimal(), ErrSplitTotal)
	}
	return nil
}

// scanEntry reads one row selected with selectEntry.
func scanEntry(stmt *sqlite.Stmt, cur money.Currency) (Entry, error) {
	txn, err := scanTransaction(stmt, cur)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Transaction: txn,
		Category:    stmt.GetText("category"),
		SplitCount:  int(stmt.GetInt64("split_count")),
	}, nil
}

// scanTransaction reads the transaction columns of the current row.
func scanTransaction(stmt *sqlite.Stmt, cur money.Currency) (Transaction, error) {
	t := Transaction{
		ID:          stmt.GetInt64("id"),
		AccountID:   stmt.GetInt64("account_id"),
		Date:        stmt.GetText("date"),
		Payee:       stmt.GetText("payee"),
		Memo:        stmt.GetText("memo"),
		Status:      Status(stmt.GetText("status")),
		CheckNumber: stmt.GetText("check_number"),
	}

	var err error
	if t.Amount, err = storage.ColumnMoney(stmt, "amount", cur); err != nil {
		return Transaction{}, err
	}
	if t.CreatedAt, err = storage.ColumnTime(stmt, "created_at"); err != nil {
		return Transaction{}, err
	}
	if t.UpdatedAt, err = storage.ColumnTime(stmt, "updated_at"); err != nil {
		return Transaction{}, err
	}
	return t, nil
}
