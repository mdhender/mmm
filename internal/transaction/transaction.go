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

	// ErrConflict is returned when a write would overwrite a change made since
	// the writer last read the record (SPECIFICATION.md CO-3). It is not a
	// failure to retry blindly: the caller has to say so, because the two edits
	// may well disagree about what the transaction is.
	ErrConflict = cerrs.Error("the transaction changed since it was read")

	// ErrReconciled is returned when a change is asked of a reconciled
	// transaction. Finishing a reconciliation records a fact, and the register
	// does not rewrite it afterwards (RC-3).
	ErrReconciled = cerrs.Error("a reconciled transaction cannot be changed from the register")
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

// SetStatus changes a transaction's status against the concurrency token it was
// read with.
//
// seen is the UpdatedAt the caller read. If the stored value has moved on, the
// write is refused with ErrConflict rather than applied: the household may have
// the same register open in several tabs, and discarding somebody's change
// without saying so is precisely the quiet loss CO-3 forbids.
//
// A reconciled transaction is refused with ErrReconciled. Cleared and reconciled
// are different facts -- one says the bank showed it, the other says a finished
// reconciliation recorded it -- and clearing a reconciled row from the register
// would rewrite the second (RC-3).
func SetStatus(ctx context.Context, store *storage.Store, acct account.Account, id int64, status Status, seen time.Time) (txn Transaction, err error) {
	if !status.Valid() {
		return Transaction{}, fmt.Errorf("%q: %w", status, ErrInvalidStatus)
	}
	if status == Reconciled {
		// Reconciling is a reconciliation's job, not a register toggle's (RC-3).
		return Transaction{}, fmt.Errorf("%q: %w", status, ErrInvalidStatus)
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Transaction{}, fmt.Errorf("set status: %w", err)
	}
	defer store.Put(conn)

	// The read and the write share one immediate transaction, so the token
	// cannot move between checking it and acting on it. Doing the UPDATE first
	// and asking afterwards why it matched nothing would be a second query
	// against a row that may have changed again in between.
	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Transaction{}, fmt.Errorf("set status: %w", err)
	}
	defer endTx(&err)

	current, err := get(conn, acct, id)
	if err != nil {
		return Transaction{}, err
	}
	if current.Status == Reconciled {
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrReconciled)
	}
	if !current.UpdatedAt.Equal(seen.UTC().Truncate(time.Microsecond)) {
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}
	if current.Status == status {
		// Already there. Say so by doing nothing rather than by burning a token
		// and making every other open tab stale for no change.
		return current, nil
	}

	stmt, err := conn.Prepare(`
UPDATE transactions SET status = $status, updated_at = $updated_at
 WHERE id = $id AND updated_at = $seen
RETURNING id, account_id, date, payee, memo, amount, status, check_number,
          created_at, updated_at;`)
	if err != nil {
		return Transaction{}, fmt.Errorf("set status: %w", err)
	}
	stmt.SetText("$status", string(status))
	storage.BindTime(stmt, "$updated_at", storage.NextUpdatedAt(current.UpdatedAt, time.Now()))
	stmt.SetInt64("$id", id)
	storage.BindTime(stmt, "$seen", current.UpdatedAt)

	hasRow, err := stmt.Step()
	if err != nil {
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("set status: %w", err)
	}
	if !hasRow {
		// The WHERE still carries the token, so this is reachable only if the row
		// moved between the read above and here. It cannot happen inside an
		// immediate transaction, and it is checked anyway: the alternative to
		// noticing is overwriting.
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}
	txn, err = scanTransaction(stmt, acct.Currency)
	if resetErr := stmt.Reset(); err == nil {
		err = resetErr
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("set status: %w", err)
	}
	return txn, nil
}

// SplitDetail is one split with the name of the category it names.
//
// It is a read type, standing to Split as Entry stands to Transaction: a form
// fills a box with a name, and the name is not on Split because Split is the
// write type and a field the writer ignores is a second source of truth waiting
// to disagree with the first.
type SplitDetail struct {
	Split

	// Category is the category's name. It is empty when CategoryID is zero,
	// which is a part of the amount assigned to no category yet -- what the
	// nullable column is for.
	Category string
}

// Detail is one transaction with the parts it is divided into.
//
// It is not an Entry, and the difference is the balance: a running balance is a
// property of a row's position in a register, not of a transaction, and an Entry
// carrying a meaningless one would be a number waiting to be printed by mistake.
//
// It carries the splits themselves, where an Entry carries only their count. The
// register needs the count; a form that divides a transaction needs the rows.
type Detail struct {
	Transaction

	// Splits are the transaction's parts in id order. None means the
	// transaction is uncategorized, which is a normal state and not an error.
	Splits []SplitDetail
}

// IsSplit reports whether the transaction is divided among more than one
// category.
func (d Detail) IsSplit() bool { return len(d.Splits) > 1 }

// SplitCount is how many parts the transaction is divided into.
func (d Detail) SplitCount() int { return len(d.Splits) }

// Get reads one of acct's transactions with its splits.
//
// It is what an edit form is built from, so it is scoped to the account for the
// reason get is: the id comes from a URL, and a URL can be edited. An id
// belonging to another account reads as ErrNotFound rather than as somebody
// else's transaction.
func Get(ctx context.Context, store *storage.Store, acct account.Account, id int64) (Detail, error) {
	conn, err := store.Conn(ctx)
	if err != nil {
		return Detail{}, fmt.Errorf("get transaction: %w", err)
	}
	defer store.Put(conn)

	txn, err := get(conn, acct, id)
	if err != nil {
		return Detail{}, err
	}
	splits, err := loadSplitDetails(conn, acct, id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Transaction: txn, Splits: splits}, nil
}

// get reads one of acct's transactions on an open connection.
//
// It is scoped to the account so an id belonging to another account reads as
// missing rather than as somebody else's row: the id comes from a URL, and a URL
// can be edited.
func get(conn *sqlite.Conn, acct account.Account, id int64) (Transaction, error) {
	stmt, err := conn.Prepare(`
SELECT id, account_id, date, payee, memo, amount, status, check_number,
       created_at, updated_at
  FROM transactions WHERE id = $id AND account_id = $account_id;`)
	if err != nil {
		return Transaction{}, fmt.Errorf("get transaction: %w", err)
	}
	stmt.SetInt64("$id", id)
	stmt.SetInt64("$account_id", acct.ID)
	defer stmt.Reset()

	hasRow, err := stmt.Step()
	if err != nil {
		return Transaction{}, fmt.Errorf("get transaction: %w", err)
	}
	if !hasRow {
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrNotFound)
	}
	return scanTransaction(stmt, acct.Currency)
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

// Edit describes the change to make to a transaction.
//
// Status is deliberately absent. Clearing a transaction is a separate fact about
// the bank rather than a correction to what was entered, and SetStatus owns it
// (RC-3). An edit that also silently marked a row cleared would be two writes
// wearing one button.
type Edit struct {
	Date        string
	Payee       string
	Memo        string
	Amount      money.Money
	CheckNumber string

	// Splits replaces the transaction's splits. Nil leaves them exactly as they
	// are, which is not the same as an empty slice: the empty slice removes
	// every split and leaves the transaction uncategorized.
	//
	// The distinction is what lets a caller that does not understand a
	// transaction's categories -- the register's edit form, faced with a
	// transaction split three ways -- change the payee without flattening them.
	// Leaving them alone does not exempt them from the invariant: a new amount
	// still has to match the splits already stored, so changing the amount of a
	// split transaction is refused with ErrSplitTotal rather than quietly
	// breaking it.
	Splits *[]Split
}

// Update changes a transaction against the concurrency token it was read with.
//
// seen is the UpdatedAt the caller read, and the rules are SetStatus's: a token
// that has moved on is ErrConflict, and a reconciled transaction is ErrReconciled
// on every field, because a finished reconciliation records a fact the register
// does not rewrite (RC-3). Nothing is written in either case.
//
// The parent row and the splits are changed inside one immediate transaction, so
// an edit that would leave the splits disagreeing with the amount leaves the
// stored transaction untouched instead of half-corrected.
func Update(ctx context.Context, store *storage.Store, acct account.Account, id int64, e Edit, seen time.Time) (txn Transaction, err error) {
	if _, err := time.Parse(DateLayout, e.Date); err != nil {
		return Transaction{}, fmt.Errorf("%q: %w", e.Date, ErrInvalidDate)
	}
	if e.Amount.Currency() != acct.Currency {
		return Transaction{}, fmt.Errorf("amount is %s, account %q is %s: %w",
			e.Amount.Currency(), acct.Name, acct.Currency, money.ErrCurrencyMismatch)
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	defer store.Put(conn)

	// One immediate transaction over the read and both writes, for SetStatus's
	// reason: the token must not be able to move between being checked and being
	// acted on, and the splits must not be replaced against a row somebody else
	// has since changed.
	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	defer endTx(&err)

	current, err := get(conn, acct, id)
	if err != nil {
		return Transaction{}, err
	}
	if current.Status == Reconciled {
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrReconciled)
	}
	if !current.UpdatedAt.Equal(seen.UTC().Truncate(time.Microsecond)) {
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}

	stored, err := loadSplits(conn, acct, id)
	if err != nil {
		return Transaction{}, err
	}
	// A nil Splits means the stored set is the set: the invariant is then checked
	// against it, and the comparison below finds nothing to replace.
	wanted := stored
	if e.Splits != nil {
		wanted = *e.Splits
	}
	if err := checkSplitTotal(e.Amount, wanted); err != nil {
		return Transaction{}, err
	}

	if unchangedTransaction(current, e) && sameSplits(stored, wanted) {
		// Nothing to write. Saying so by doing nothing is not laziness: a write
		// here would move the token and make every other tab on this register
		// stale in exchange for no change at all.
		return current, nil
	}

	stmt, err := conn.Prepare(`
UPDATE transactions
   SET date = $date, payee = $payee, memo = $memo, amount = $amount,
       check_number = $check_number, updated_at = $updated_at
 WHERE id = $id AND updated_at = $seen
RETURNING id, account_id, date, payee, memo, amount, status, check_number,
          created_at, updated_at;`)
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	stmt.SetText("$date", e.Date)
	stmt.SetText("$payee", e.Payee)
	stmt.SetText("$memo", e.Memo)
	storage.BindMoney(stmt, "$amount", e.Amount)
	stmt.SetText("$check_number", e.CheckNumber)
	storage.BindTime(stmt, "$updated_at", storage.NextUpdatedAt(current.UpdatedAt, time.Now()))
	stmt.SetInt64("$id", id)
	storage.BindTime(stmt, "$seen", current.UpdatedAt)

	hasRow, err := stmt.Step()
	if err != nil {
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	if !hasRow {
		// Unreachable inside an immediate transaction, and checked anyway: the
		// alternative to noticing is overwriting.
		_ = stmt.Reset()
		return Transaction{}, fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}
	txn, err = scanTransaction(stmt, acct.Currency)
	if resetErr := stmt.Reset(); err == nil {
		err = resetErr
	}
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}

	if !sameSplits(stored, wanted) {
		if err := replaceSplits(conn, id, wanted); err != nil {
			return Transaction{}, err
		}
	}
	return txn, nil
}

// Delete removes a transaction and its splits.
//
// seen is the token, and the write is refused on a stale one exactly as an edit
// is: a tab that has been open since before somebody else changed a transaction
// is not looking at the transaction it is asking to remove (CO-3). A reconciled
// transaction is refused outright (RC-3).
//
// The splits go with the parent by ON DELETE CASCADE, which is real only because
// storage sets foreign_keys = ON on every connection. There is no tombstone and
// no reversing entry: a deleted transaction leaves the register and the balance,
// and a deletion regretted later is what the backups are for (BK-1).
func Delete(ctx context.Context, store *storage.Store, acct account.Account, id int64, seen time.Time) (err error) {
	conn, err := store.Conn(ctx)
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	defer store.Put(conn)

	endTx, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	defer endTx(&err)

	current, err := get(conn, acct, id)
	if err != nil {
		return err
	}
	if current.Status == Reconciled {
		return fmt.Errorf("transaction %d: %w", id, ErrReconciled)
	}
	if !current.UpdatedAt.Equal(seen.UTC().Truncate(time.Microsecond)) {
		return fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}

	stmt, err := conn.Prepare(`
DELETE FROM transactions WHERE id = $id AND account_id = $account_id AND updated_at = $seen;`)
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	stmt.SetInt64("$id", id)
	stmt.SetInt64("$account_id", acct.ID)
	storage.BindTime(stmt, "$seen", current.UpdatedAt)

	_, err = stmt.Step()
	if resetErr := stmt.Reset(); err == nil {
		err = resetErr
	}
	if err != nil {
		return fmt.Errorf("delete transaction: %w", err)
	}
	if conn.Changes() == 0 {
		// The row was read a few statements ago inside this transaction, so this
		// cannot happen. It is checked because a delete that matched nothing and
		// reported success would be the quietest possible lie.
		return fmt.Errorf("transaction %d: %w", id, ErrConflict)
	}
	return nil
}

// unchangedTransaction reports whether e would leave the stored row as it is.
func unchangedTransaction(current Transaction, e Edit) bool {
	return current.Date == e.Date &&
		current.Payee == e.Payee &&
		current.Memo == e.Memo &&
		current.CheckNumber == e.CheckNumber &&
		current.Amount.Currency() == e.Amount.Currency() &&
		current.Amount.Amount() == e.Amount.Amount()
}

// sameSplits reports whether two split sets are the same, in order.
//
// Order counts because the register shows the first split's category on a row
// that has only one, and because reordering is a change the household made and
// should see kept.
func sameSplits(a, b []Split) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].CategoryID != b[i].CategoryID ||
			a[i].Memo != b[i].Memo ||
			a[i].Amount.Currency() != b[i].Amount.Currency() ||
			a[i].Amount.Amount() != b[i].Amount.Amount() {
			return false
		}
	}
	return true
}

// loadSplits reads a transaction's splits in id order on an open connection.
//
// It is loadSplitDetails without the names, rather than a second query, so the
// set a write compares against and the set a form displays can never come back
// in a different order.
func loadSplits(conn *sqlite.Conn, acct account.Account, txnID int64) ([]Split, error) {
	details, err := loadSplitDetails(conn, acct, txnID)
	if err != nil {
		return nil, err
	}
	if len(details) == 0 {
		return nil, nil
	}
	splits := make([]Split, 0, len(details))
	for _, d := range details {
		splits = append(splits, d.Split)
	}
	return splits, nil
}

// loadSplitDetails reads a transaction's splits, with category names, in id
// order on an open connection.
//
// The join is a LEFT JOIN because category_id is nullable: a part assigned to no
// category yet is a row with no name, not a missing row.
func loadSplitDetails(conn *sqlite.Conn, acct account.Account, txnID int64) ([]SplitDetail, error) {
	stmt, err := conn.Prepare(`
SELECT COALESCE(s.category_id, 0) AS category_id, s.amount, s.memo,
       COALESCE(c.name, '') AS category
  FROM splits s
  LEFT JOIN categories c ON c.id = s.category_id
 WHERE s.transaction_id = $transaction_id
 ORDER BY s.id;`)
	if err != nil {
		return nil, fmt.Errorf("read splits: %w", err)
	}
	stmt.SetInt64("$transaction_id", txnID)
	defer stmt.Reset()

	var splits []SplitDetail
	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return nil, fmt.Errorf("read splits: %w", err)
		}
		if !hasRow {
			return splits, nil
		}
		amount, err := storage.ColumnMoney(stmt, "amount", acct.Currency)
		if err != nil {
			return nil, fmt.Errorf("read splits: %w", err)
		}
		splits = append(splits, SplitDetail{
			Split: Split{
				CategoryID: stmt.GetInt64("category_id"),
				Amount:     amount,
				Memo:       stmt.GetText("memo"),
			},
			Category: stmt.GetText("category"),
		})
	}
}

// replaceSplits writes a transaction's splits, discarding whatever was there.
//
// The old rows are deleted and the new ones inserted rather than matched up and
// patched. Split ids are held by nothing -- no reconciliation, no export, no
// import -- so there is nothing for a diff to preserve, and a diff would be a
// second description of the same set to keep correct. ST-9 forbids reusing an
// id, not spending one.
func replaceSplits(conn *sqlite.Conn, txnID int64, splits []Split) error {
	del, err := conn.Prepare(`DELETE FROM splits WHERE transaction_id = $transaction_id;`)
	if err != nil {
		return fmt.Errorf("replace splits: %w", err)
	}
	del.SetInt64("$transaction_id", txnID)
	_, err = del.Step()
	if resetErr := del.Reset(); err == nil {
		err = resetErr
	}
	if err != nil {
		return fmt.Errorf("replace splits: %w", err)
	}

	for i, s := range splits {
		stmt, err := conn.Prepare(`
INSERT INTO splits (transaction_id, category_id, amount, memo)
VALUES ($transaction_id, $category_id, $amount, $memo);`)
		if err != nil {
			return fmt.Errorf("replace splits: split %d: %w", i+1, err)
		}
		stmt.SetInt64("$transaction_id", txnID)
		if s.CategoryID == 0 {
			stmt.SetNull("$category_id")
		} else {
			stmt.SetInt64("$category_id", s.CategoryID)
		}
		storage.BindMoney(stmt, "$amount", s.Amount)
		stmt.SetText("$memo", s.Memo)

		_, err = stmt.Step()
		if resetErr := stmt.Reset(); err == nil {
			err = resetErr
		}
		if err != nil {
			return fmt.Errorf("replace splits: split %d: %w", i+1, err)
		}
	}
	return nil
}
