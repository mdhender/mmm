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

// BackupAppID is what a backup carries instead. It is the ASCII encoding of
// "MMM~" (0x4d, 0x4d, 0x4d, 0x7e): the same three letters, so a file inspected
// with any SQLite tool still says which program wrote it, and a fourth byte that
// says which kind of file it is.
//
// A backup is a byte-for-byte capable checkbook -- VACUUM INTO copies the header
// along with everything else -- so before this existed the only thing telling
// the two apart was the reader remembering to tick a box. That is not a guard.
// The application_id is, because it travels with the file: through a rename, a
// copy, a move to another disk. Open refuses it (BK-6), OpenReadOnly accepts it,
// and backup.Restore is how records inside one come back into use.
//
// Like AppID, this value must never change.
const BackupAppID int32 = 0x4d4d4d7e

// isoDate matches an ISO 8601 calendar date, "YYYY-MM-DD".
//
// SQLite has no date type, so dates are TEXT in this format: it sorts
// chronologically as a string, which is what the register and every date range
// query depend on. GLOB checks the shape only; a well-formed but impossible date
// such as "2026-02-31" is the domain layer's problem, not the schema's.
const isoDate = `'[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'`

// isoTimestamp matches TimeLayout: an instant in UTC to microsecond precision.
// The trailing Z is required, so a local time or a value carrying an offset
// cannot be stored (SPECIFICATION.md ST-7).
const isoTimestamp = `'[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T` +
	`[0-9][0-9]:[0-9][0-9]:[0-9][0-9].[0-9][0-9][0-9][0-9][0-9][0-9]Z'`

// MigrationCount reports how many migrations the schema defines. A fully
// migrated database has this value in user_version.
func MigrationCount() int { return len(schema.Migrations) }

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
	// Migration 3 rebuilds tables, which requires foreign keys off for the
	// duration; sqlitemigration restores the setting afterwards.
	MigrationOptions: []*sqlitemigration.MigrationOptions{
		nil,
		nil,
		{DisableForeignKeys: true},
	},
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
		// 2: record timestamps, and the optimistic concurrency token.
		//
		// updated_at is the CO-3 conflict token: an update carries the value it
		// read and matches on it, so a write from a stale browser tab affects no
		// rows instead of silently discarding someone else's edit. See
		// NextUpdatedAt in time.go for why the value is forced to increase.
		//
		// Instants are stored in UTC (ST-7). Note the distinction from the
		// existing date columns: transactions.date and statement_date are
		// calendar dates, not instants, and are deliberately timezone-free --
		// converting them would let a transaction change day for a user west of
		// UTC.
		//
		// splits get no token. A split is edited as part of its transaction, so
		// the transaction is the aggregate root and its updated_at guards the
		// whole record. Giving splits an independent token would invite
		// compare-and-set against a child while its parent moves underneath.
		//
		// SQLite only accepts a constant DEFAULT in ADD COLUMN, hence the epoch
		// placeholder and the backfill that follows. New rows are expected to
		// set both columns explicitly.
		`
ALTER TABLE accounts        ADD COLUMN created_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE accounts        ADD COLUMN updated_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE transactions    ADD COLUMN created_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE transactions    ADD COLUMN updated_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE categories      ADD COLUMN created_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE categories      ADD COLUMN updated_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE reconciliations ADD COLUMN created_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';
ALTER TABLE reconciliations ADD COLUMN updated_at TEXT NOT NULL DEFAULT '1970-01-01T00:00:00.000000Z';

UPDATE accounts        SET created_at = ` + nowSQL + `, updated_at = ` + nowSQL + `;
UPDATE transactions    SET created_at = ` + nowSQL + `, updated_at = ` + nowSQL + `;
UPDATE categories      SET created_at = ` + nowSQL + `, updated_at = ` + nowSQL + `;
UPDATE reconciliations SET created_at = ` + nowSQL + `, updated_at = ` + nowSQL + `;

CREATE INDEX transactions_updated_at ON transactions (updated_at);
`,
		// 3: never reuse a deleted id, and constrain the timestamp columns.
		//
		// A plain INTEGER PRIMARY KEY is an alias for the rowid, and SQLite
		// assigns max(rowid)+1. Delete the newest transaction and the next insert
		// takes its id back. That silently repoints anything holding the old one
		// -- a browser tab, a bookmarked URL, an exported file, a reconciliation
		// -- at an unrelated record. AUTOINCREMENT tracks the high-water mark in
		// sqlite_sequence so an id is never handed out twice.
		//
		// AUTOINCREMENT cannot be added by ALTER TABLE, so this is the documented
		// table rebuild: create, copy, drop, rename. legacy_alter_table keeps
		// RENAME from rewriting or validating references in tables that are
		// mid-rebuild; it is restored at the end.
		//
		// Rebuilding also lets the timestamp columns finally take CHECK
		// constraints and a real DEFAULT. A non-constant default is legal in
		// CREATE TABLE and only forbidden in ADD COLUMN, which is why migration 2
		// had to settle for an epoch placeholder. Any insert that omits the
		// columns now gets a valid timestamp rather than 1970 or a failure.
		`
PRAGMA legacy_alter_table = ON;

CREATE TABLE new_categories (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL COLLATE NOCASE UNIQUE,
    created_at TEXT NOT NULL DEFAULT (` + nowSQL + `)
                    CHECK (created_at GLOB ` + isoTimestamp + `),
    updated_at TEXT NOT NULL DEFAULT (` + nowSQL + `)
                    CHECK (updated_at GLOB ` + isoTimestamp + `)
);

CREATE TABLE new_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    type            TEXT    NOT NULL
                            CHECK (type IN ('checking', 'savings', 'credit', 'cash')),
    currency        TEXT    NOT NULL
                            CHECK (length(currency) BETWEEN 3 AND 16),
    opening_balance INTEGER NOT NULL DEFAULT 0
                            CHECK (typeof(opening_balance) = 'integer'),
    closed_at       TEXT        NULL
                            CHECK (closed_at IS NULL OR closed_at GLOB ` + isoDate + `),
    created_at      TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                            CHECK (created_at GLOB ` + isoTimestamp + `),
    updated_at      TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                            CHECK (updated_at GLOB ` + isoTimestamp + `)
);

CREATE TABLE new_transactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id   INTEGER NOT NULL REFERENCES accounts (id),
    date         TEXT    NOT NULL CHECK (date GLOB ` + isoDate + `),
    payee        TEXT    NOT NULL DEFAULT '',
    memo         TEXT    NOT NULL DEFAULT '',
    amount       INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    status       TEXT    NOT NULL DEFAULT 'uncleared'
                         CHECK (status IN ('uncleared', 'cleared', 'reconciled')),
    check_number TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                         CHECK (created_at GLOB ` + isoTimestamp + `),
    updated_at   TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                         CHECK (updated_at GLOB ` + isoTimestamp + `)
);

CREATE TABLE new_splits (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    category_id    INTEGER     NULL REFERENCES categories (id),
    amount         INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    memo           TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE new_reconciliations (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id        INTEGER NOT NULL REFERENCES accounts (id),
    statement_date    TEXT    NOT NULL CHECK (statement_date GLOB ` + isoDate + `),
    statement_balance INTEGER NOT NULL
                              CHECK (typeof(statement_balance) = 'integer'),
    created_at        TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                              CHECK (created_at GLOB ` + isoTimestamp + `),
    updated_at        TEXT    NOT NULL DEFAULT (` + nowSQL + `)
                              CHECK (updated_at GLOB ` + isoTimestamp + `)
);

INSERT INTO new_categories (id, name, created_at, updated_at)
    SELECT id, name, created_at, updated_at FROM categories;

INSERT INTO new_accounts (id, name, type, currency, opening_balance, closed_at, created_at, updated_at)
    SELECT id, name, type, currency, opening_balance, closed_at, created_at, updated_at FROM accounts;

INSERT INTO new_transactions (id, account_id, date, payee, memo, amount, status, check_number, created_at, updated_at)
    SELECT id, account_id, date, payee, memo, amount, status, check_number, created_at, updated_at FROM transactions;

INSERT INTO new_splits (id, transaction_id, category_id, amount, memo)
    SELECT id, transaction_id, category_id, amount, memo FROM splits;

INSERT INTO new_reconciliations (id, account_id, statement_date, statement_balance, created_at, updated_at)
    SELECT id, account_id, statement_date, statement_balance, created_at, updated_at FROM reconciliations;

DROP TABLE splits;
DROP TABLE reconciliations;
DROP TABLE transactions;
DROP TABLE accounts;
DROP TABLE categories;

ALTER TABLE new_categories      RENAME TO categories;
ALTER TABLE new_accounts        RENAME TO accounts;
ALTER TABLE new_transactions    RENAME TO transactions;
ALTER TABLE new_splits          RENAME TO splits;
ALTER TABLE new_reconciliations RENAME TO reconciliations;

CREATE INDEX transactions_account_date ON transactions (account_id, date);
CREATE INDEX transactions_updated_at   ON transactions (updated_at);
CREATE INDEX splits_transaction        ON splits (transaction_id);
CREATE INDEX reconciliations_account   ON reconciliations (account_id, statement_date);

PRAGMA legacy_alter_table = OFF;
`,
	},
}
