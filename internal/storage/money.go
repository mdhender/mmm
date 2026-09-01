// Copyright (c) 2026 Michael D Henderson.

package storage

import (
	"fmt"

	"zombiezen.com/go/sqlite"

	"github.com/mdhender/mmm/internal/money"
)

// BindMoney binds m to a named parameter as an integer count of minor units.
//
// ZombieZen is deliberately not a database/sql driver, so there is no Valuer or
// Scanner hook: money.Money cannot encode itself and every monetary bind goes
// through this function.
//
// Do not reach for money.Money's MarshalJSON here. JSON is the wire and export
// format; an amount column stored as JSON cannot be summed, compared, or indexed
// by SQLite, which is most of what the register needs from it.
//
// Never pass a money.Money to sqlitex.ExecOptions.Args instead of calling this.
// Args maps unknown kinds with fmt.Sprint, and money.Money has a String method,
// so the bind silently succeeds and writes the text "USD 125.95" into an amount
// column. Reading that back with ColumnInt64 yields 0. The typeof() CHECK
// constraints in schema.go exist to turn that silent corruption into an error.
func BindMoney(stmt *sqlite.Stmt, param string, m money.Money) {
	stmt.SetInt64(param, m.Amount())
}

// ColumnMoney reads a named column as an amount denominated in cur.
//
// The currency is supplied by the caller because the schema keeps it on the
// account rather than on each row. Reconstruction goes through money.NewMinor,
// so a currency that was never registered fails loudly here instead of decoding
// at the wrong scale.
func ColumnMoney(stmt *sqlite.Stmt, col string, cur money.Currency) (money.Money, error) {
	m, err := money.NewMinor(stmt.GetInt64(col), cur)
	if err != nil {
		return money.Money{}, fmt.Errorf("column %s: %w", col, err)
	}
	return m, nil
}
