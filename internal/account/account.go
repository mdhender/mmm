// Copyright (c) 2026 Michael D Henderson.

// Package account reads and writes the accounts a household keeps.
//
// The package talks to the database through internal/storage and knows nothing
// about HTTP, templates, or the terminal (SPECIFICATION.md TS-2), so the web UI,
// the TUI, and any future CLI subcommand share one definition of what an account
// is rather than growing three.
package account

import (
	"context"
	"fmt"
	"time"

	"zombiezen.com/go/sqlite"

	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

const (
	// ErrNotFound is returned when no account has the requested id.
	ErrNotFound = cerrs.Error("account not found")

	// ErrMissingName is returned when an account is created without a name.
	ErrMissingName = cerrs.Error("missing account name")

	// ErrInvalidType is returned when an account type is not one the schema
	// accepts.
	ErrInvalidType = cerrs.Error("invalid account type")
)

// Type is the kind of account. The values are exactly the ones the schema's
// CHECK constraint allows; adding one means a migration, not a new constant.
type Type string

const (
	Checking Type = "checking"
	Savings  Type = "savings"
	Credit   Type = "credit"
	Cash     Type = "cash"
)

// Valid reports whether t is a type the schema accepts.
func (t Type) Valid() bool {
	switch t {
	case Checking, Savings, Credit, Cash:
		return true
	}
	return false
}

// Account is one account in the checkbook.
type Account struct {
	ID   int64
	Name string
	Type Type

	// Currency is carried by the account rather than by each amount, which is
	// what makes summing a register meaningful. Every Money read out of this
	// account's rows is denominated in it.
	Currency money.Currency

	// OpeningBalance is the balance the register starts from, before the first
	// transaction.
	OpeningBalance money.Money

	// ClosedOn is a calendar date, "YYYY-MM-DD", or "" while the account is
	// open. It is deliberately a string and not a time.Time: a calendar date is
	// not an instant and must not be shifted by a timezone (SPECIFICATION.md
	// ST-8).
	ClosedOn string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// IsClosed reports whether the account has been closed.
func (a Account) IsClosed() bool { return a.ClosedOn != "" }

// columns is the select list every account query shares, so scanAccount can read
// them all by name.
const columns = `
    id, name, type, currency, opening_balance, closed_at, created_at, updated_at`

// List returns every account, open ones first and alphabetically within each
// group, which is the order the register's account list wants.
func List(ctx context.Context, store *storage.Store) ([]Account, error) {
	conn, err := store.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`SELECT` + columns + `
FROM accounts
ORDER BY closed_at IS NOT NULL, name;`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer stmt.Reset()

	var accounts []Account
	for {
		hasRow, err := stmt.Step()
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		if !hasRow {
			return accounts, nil
		}
		a, err := scanAccount(stmt)
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		accounts = append(accounts, a)
	}
}

// Get returns the account with the given id, or ErrNotFound.
func Get(ctx context.Context, store *storage.Store, id int64) (Account, error) {
	conn, err := store.Conn(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("get account %d: %w", id, err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`SELECT` + columns + `
FROM accounts
WHERE id = $id;`)
	if err != nil {
		return Account{}, fmt.Errorf("get account %d: %w", id, err)
	}
	defer stmt.Reset()
	stmt.SetInt64("$id", id)

	hasRow, err := stmt.Step()
	if err != nil {
		return Account{}, fmt.Errorf("get account %d: %w", id, err)
	}
	if !hasRow {
		return Account{}, fmt.Errorf("%d: %w", id, ErrNotFound)
	}
	a, err := scanAccount(stmt)
	if err != nil {
		return Account{}, fmt.Errorf("get account %d: %w", id, err)
	}
	return a, nil
}

// New describes an account to create.
type New struct {
	Name           string
	Type           Type
	Currency       money.Currency
	OpeningBalance money.Money
}

// Create inserts an account and returns it as stored.
//
// The opening balance must already be denominated in Currency; mixing them is
// rejected here rather than producing a register whose amounts do not agree with
// its own totals.
func Create(ctx context.Context, store *storage.Store, n New) (Account, error) {
	if n.Name == "" {
		return Account{}, ErrMissingName
	}
	if !n.Type.Valid() {
		return Account{}, fmt.Errorf("%q: %w", n.Type, ErrInvalidType)
	}
	if n.OpeningBalance == (money.Money{}) {
		zero, err := money.Zero(n.Currency)
		if err != nil {
			return Account{}, fmt.Errorf("create account %q: %w", n.Name, err)
		}
		n.OpeningBalance = zero
	}
	if n.OpeningBalance.Currency() != n.Currency {
		return Account{}, fmt.Errorf("create account %q: opening balance is %s, account is %s: %w",
			n.Name, n.OpeningBalance.Currency(), n.Currency, money.ErrCurrencyMismatch)
	}

	conn, err := store.Conn(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("create account %q: %w", n.Name, err)
	}
	defer store.Put(conn)

	stmt, err := conn.Prepare(`
INSERT INTO accounts (name, type, currency, opening_balance)
VALUES ($name, $type, $currency, $opening_balance)
RETURNING` + columns + `;`)
	if err != nil {
		return Account{}, fmt.Errorf("create account %q: %w", n.Name, err)
	}
	defer stmt.Reset()

	stmt.SetText("$name", n.Name)
	stmt.SetText("$type", string(n.Type))
	stmt.SetText("$currency", string(n.Currency))
	// Never bind a money.Money through a reflection-based argument list: it has
	// a String method, so the amount would be stored as text.
	storage.BindMoney(stmt, "$opening_balance", n.OpeningBalance)

	hasRow, err := stmt.Step()
	if err != nil {
		return Account{}, fmt.Errorf("create account %q: %w", n.Name, err)
	}
	if !hasRow {
		return Account{}, fmt.Errorf("create account %q: insert returned no row", n.Name)
	}
	a, err := scanAccount(stmt)
	if err != nil {
		return Account{}, fmt.Errorf("create account %q: %w", n.Name, err)
	}
	return a, nil
}

// scanAccount reads one row selected with columns.
func scanAccount(stmt *sqlite.Stmt) (Account, error) {
	a := Account{
		ID:       stmt.GetInt64("id"),
		Name:     stmt.GetText("name"),
		Type:     Type(stmt.GetText("type")),
		Currency: money.Currency(stmt.GetText("currency")),
		// GetText reports NULL as "", which is exactly what an open account
		// means here.
		ClosedOn: stmt.GetText("closed_at"),
	}

	var err error
	if a.OpeningBalance, err = storage.ColumnMoney(stmt, "opening_balance", a.Currency); err != nil {
		return Account{}, err
	}
	if a.CreatedAt, err = storage.ColumnTime(stmt, "created_at"); err != nil {
		return Account{}, err
	}
	if a.UpdatedAt, err = storage.ColumnTime(stmt, "updated_at"); err != nil {
		return Account{}, err
	}
	return a, nil
}
