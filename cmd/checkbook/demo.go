// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"context"
	"fmt"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// seedDemo fills an in-memory database with a small sample household so the
// register can be looked at before the program can enter transactions.
//
// It exists only in this command, never in the domain packages, and it only ever
// runs against the store built by -demo, which is held in memory and written to
// no file. Nothing here is a fixture the real application depends on.
func seedDemo(ctx context.Context, store *storage.Store) error {
	usd := money.USD

	amount := func(s string) money.Money {
		m, err := money.ParseDecimal(s, usd)
		if err != nil {
			// The literals below are written here, so this is a programming
			// error rather than anything the user can cause.
			panic(fmt.Sprintf("demo amount %q: %v", s, err))
		}
		return m
	}

	accounts := []account.New{
		{Name: "Checking", Type: account.Checking, Currency: usd, OpeningBalance: amount("3812.44")},
		{Name: "Savings", Type: account.Savings, Currency: usd, OpeningBalance: amount("9150.00")},
		{Name: "Visa", Type: account.Credit, Currency: usd, OpeningBalance: amount("-412.08")},
	}
	created := make(map[string]account.Account, len(accounts))
	for _, n := range accounts {
		a, err := account.Create(ctx, store, n)
		if err != nil {
			return fmt.Errorf("sample data: %w", err)
		}
		created[a.Name] = a
	}

	categories := make(map[string]int64)
	for _, name := range []string{"Groceries", "Utilities", "Household", "Salary", "Transfer", "Dining"} {
		c, err := category.Ensure(ctx, store, name)
		if err != nil {
			return fmt.Errorf("sample data: %w", err)
		}
		categories[name] = c.ID
	}

	// One split transaction, so the register shows what a divided entry looks
	// like: the row names no single category, and the parts total the amount.
	entries := []struct {
		account string
		txn     transaction.New
	}{
		{"Checking", transaction.New{
			Date: "2026-08-14", Payee: "Acme Manufacturing", Memo: "August salary",
			Amount: amount("2480.16"), Status: transaction.Reconciled,
			Splits: []transaction.Split{{CategoryID: categories["Salary"], Amount: amount("2480.16")}},
		}},
		{"Checking", transaction.New{
			Date: "2026-08-19", Payee: "City Power & Water", CheckNumber: "1042",
			Amount: amount("-186.30"), Status: transaction.Reconciled,
			Splits: []transaction.Split{{CategoryID: categories["Utilities"], Amount: amount("-186.30")}},
		}},
		{"Checking", transaction.New{
			Date: "2026-08-27", Payee: "Riba Smith", Memo: "weekly shop and a mop",
			Amount: amount("-84.17"), Status: transaction.Cleared,
			Splits: []transaction.Split{
				{CategoryID: categories["Groceries"], Amount: amount("-71.22")},
				{CategoryID: categories["Household"], Amount: amount("-12.95"), Memo: "mop"},
			},
		}},
		{"Checking", transaction.New{
			Date: "2026-08-28", Payee: "Banco General", Memo: "transfer to savings",
			Amount: amount("-1000.00"), Status: transaction.Cleared,
			Splits: []transaction.Split{{CategoryID: categories["Transfer"], Amount: amount("-1000.00")}},
		}},
		{"Checking", transaction.New{
			Date: "2026-08-29", Payee: "Felipe Motta", Amount: amount("-36.42"),
			Splits: []transaction.Split{{CategoryID: categories["Dining"], Amount: amount("-36.42")}},
		}},
		{"Checking", transaction.New{
			Date: "2026-08-30", Payee: "Panaderia Ana", Memo: "no receipt yet",
			Amount: amount("-14.75"),
		}},
		{"Savings", transaction.New{
			Date: "2026-08-28", Payee: "Banco General", Memo: "transfer from checking",
			Amount: amount("1000.00"), Status: transaction.Cleared,
			Splits: []transaction.Split{{CategoryID: categories["Transfer"], Amount: amount("1000.00")}},
		}},
		{"Visa", transaction.New{
			Date: "2026-08-22", Payee: "Libreria Argosy", Amount: amount("-52.90"),
			Status: transaction.Cleared,
		}},
		{"Visa", transaction.New{
			Date: "2026-08-26", Payee: "Estacion Delta", Memo: "fuel", Amount: amount("-63.10"),
		}},
	}

	for _, e := range entries {
		if _, err := transaction.Create(ctx, store, created[e.account], e.txn); err != nil {
			return fmt.Errorf("sample data: %s: %w", e.txn.Payee, err)
		}
	}
	return nil
}
