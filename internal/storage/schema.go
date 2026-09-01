// Copyright (c) 2026 Michael D Henderson.

package storage

import (
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// AppID is written to the database header as SQLite's application_id.
// It is the ASCII encoding of "MMM " (0x4d, 0x4d, 0x4d, 0x20).
//
// sqlitemigration refuses to migrate a database whose application_id is set to
// anything else, which is what keeps the program from writing its schema into an
// unrelated SQLite file that happens to be at the path we were given.
//
// Per SPECIFICATION.md ST-4 this value must never change.
const AppID int32 = 0x4d4d4d20

// isoDate matches an ISO 8601 calendar date, "YYYY-MM-DD".
//
// SQLite has no date type, so dates are TEXT in this format: it sorts
// chronologically as a string, which is what the register and every date range
// query depend on. GLOB checks the shape only; a well-formed but impossible date
// such as "2026-02-31" is the domain layer's problem, not the schema's.
const isoDate = `'[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'`

// schema is the migration set for the checkbook database.
//
// Migrations are append-only (SPECIFICATION.md ST-4). Never edit or reorder an
// entry that has shipped: sqlitemigration tracks progress as a count in
// user_version, so changing an applied migration silently desynchronizes every
// database that already ran it. Fix a mistake by appending a migration that
// corrects it.
//
// Each migration runs inside its own transaction and is rolled back on error.
var schema = sqlitemigration.Schema{
	AppID: AppID,
	Migrations: []string{
		// 1: the register.
		//
		// Money is stored as an integer count of minor units (SPECIFICATION.md
		// CO-1); see money.go in this package for the binding helpers. Every
		// monetary column carries a typeof() CHECK because SQLite's column
		// affinity would otherwise accept a float or a string here and quietly
		// destroy the exactness the whole program is built on.
		`
CREATE TABLE categories (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE
);

CREATE TABLE accounts (
    id              INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    type            TEXT    NOT NULL
                            CHECK (type IN ('checking', 'savings', 'credit', 'cash')),
    -- Currency lives on the account, not on each amount, so that SUM(amount)
    -- over a register is meaningful. Length matches money.validateCurrencyCode.
    currency        TEXT    NOT NULL
                            CHECK (length(currency) BETWEEN 3 AND 16),
    opening_balance INTEGER NOT NULL DEFAULT 0
                            CHECK (typeof(opening_balance) = 'integer'),
    closed_at       TEXT        NULL
                            CHECK (closed_at IS NULL OR closed_at GLOB ` + isoDate + `)
);

CREATE TABLE transactions (
    id           INTEGER PRIMARY KEY,
    account_id   INTEGER NOT NULL REFERENCES accounts (id),
    date         TEXT    NOT NULL CHECK (date GLOB ` + isoDate + `),
    payee        TEXT    NOT NULL DEFAULT '',
    memo         TEXT    NOT NULL DEFAULT '',
    amount       INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    -- 'reconciled' is set by finishing a reconciliation and is not the same
    -- thing as 'cleared' (SPECIFICATION.md RC-3).
    status       TEXT    NOT NULL DEFAULT 'uncleared'
                         CHECK (status IN ('uncleared', 'cleared', 'reconciled')),
    check_number TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX transactions_account_date ON transactions (account_id, date);

CREATE TABLE splits (
    id             INTEGER PRIMARY KEY,
    transaction_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    category_id    INTEGER     NULL REFERENCES categories (id),
    amount         INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    memo           TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX splits_transaction ON splits (transaction_id);

CREATE TABLE reconciliations (
    id                INTEGER PRIMARY KEY,
    account_id        INTEGER NOT NULL REFERENCES accounts (id),
    statement_date    TEXT    NOT NULL CHECK (statement_date GLOB ` + isoDate + `),
    statement_balance INTEGER NOT NULL
                              CHECK (typeof(statement_balance) = 'integer')
);

CREATE INDEX reconciliations_account ON reconciliations (account_id, statement_date);
`,
	},
}
