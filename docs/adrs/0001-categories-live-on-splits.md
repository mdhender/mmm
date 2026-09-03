# 1. Categories live on splits, not on transactions

| | |
| --- | --- |
| **Status** | Accepted |
| **Decided** | 2026-09-01, in the first migration (`6333f29`) |
| **Recorded** | 2026-09-02 |
| **Bears on** | ST-5, ST-9, CO-1, CO-2, CO-3, RG-1, RG-2, SC-3 |

This note was drafted before the schema settled, and used a vocabulary — *allocation*,
`transaction_record` — that the program never adopted. It has been rewritten against the model
that was actually built. The decision it records is unchanged; only the words and the table
names are.

## Context

A transaction records what happened to an account: an account, a date, a payee, an amount, a
memo, and the register metadata around them (`status`, `check_number`). What the money was *for*
is a different question, and one transaction can have more than one answer — a single card
payment at a shop is half groceries and half household goods.

There are two obvious places to put the answer, and the tempting one is a `category_id` column on
`transactions`, with child rows only for the transactions that need more than one. That gives
two representations of the same fact, and every operation that touches a category — the register
row, an edit form, an import, a report, a validation — has to ask which shape it is looking at
before it knows where to look.

RG-2 makes splitting a first-class operation rather than a special case, and SC-3 puts it in
version 1 alongside the plain register. A model in which the ordinary case and the split case are
different kinds of thing would carry that branch into every one of those features.

## Decision

**The transaction carries no category.** Every category assignment is a row in `splits`, which
holds a `category_id`, an `amount`, and a memo of its own.

> The transaction records what happened to the account. The splits explain what it was for.

An ordinary categorized transaction is a transaction with one split. A split transaction is a
transaction with several. There is no split-transaction *type* in the database; the only
difference is the number of rows.

## The schema as built

From `internal/storage/schema.go`, after the migration 3 rebuild. `nowSQL` is the ST-7 timestamp
default and the `GLOB` checks are its shape; the `typeof` checks are CO-1, which is what keeps
column affinity from quietly accepting a float where minor units belong.

```sql
CREATE TABLE transactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id   INTEGER NOT NULL REFERENCES accounts (id),
    date         TEXT    NOT NULL CHECK (date GLOB '...'),      -- YYYY-MM-DD, ST-8
    payee        TEXT    NOT NULL DEFAULT '',
    memo         TEXT    NOT NULL DEFAULT '',
    amount       INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    status       TEXT    NOT NULL DEFAULT 'uncleared'
                         CHECK (status IN ('uncleared', 'cleared', 'reconciled')),
    check_number TEXT    NOT NULL DEFAULT '',
    created_at   TEXT    NOT NULL DEFAULT (...) CHECK (created_at GLOB '...'),
    updated_at   TEXT    NOT NULL DEFAULT (...) CHECK (updated_at GLOB '...')
);

CREATE TABLE splits (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    transaction_id INTEGER NOT NULL REFERENCES transactions (id) ON DELETE CASCADE,
    category_id    INTEGER     NULL REFERENCES categories (id),
    amount         INTEGER NOT NULL CHECK (typeof(amount) = 'integer'),
    memo           TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX splits_transaction ON splits (transaction_id);
```

Four things in there are load-bearing and were not in the draft:

- **`id` is `INTEGER PRIMARY KEY AUTOINCREMENT`**, not a text id (ST-9). Without `AUTOINCREMENT`
  SQLite hands a deleted row's id to the next insert, and a bookmark, an export, or a
  reconciliation holding that id would silently come to mean a different record.
- **`category_id` is nullable.** The draft required it. See *Uncategorized* below.
- **`ON DELETE CASCADE` is real only because `prepareConn` sets `foreign_keys = ON`** on every
  connection — SQLite defaults it off, which would make the clause decorative and leave orphaned
  splits behind a deleted transaction.
- **`splits` has no `updated_at`.** That is deliberate, and it is the other half of this
  decision: a split is edited as part of its transaction, so the transaction is the aggregate
  root and its `updated_at` is the CO-3 token guarding the whole record. A per-split token would
  let two tabs each win half of one edit.

**Money is integer minor units** (CO-1): `$82.37` is `8237`, and a withdrawal of it is `-8237`.
Bind with `storage.BindMoney` and read with `storage.ColumnMoney`. Splits carry no currency
because currency lives on the account, which is what makes `SUM(amount)` over a register
meaningful.

## An ordinary categorized transaction

Account `1` is Checking; category `10` is Groceries.

```sql
INSERT INTO transactions (account_id, date, payee, memo, amount, status, check_number)
VALUES (1, '2026-09-02', 'Supermercado Rey', '', -8237, 'uncleared', '');
-- RETURNING id -> 41

INSERT INTO splits (transaction_id, category_id, amount, memo)
VALUES (41, 10, -8237, '');
```

```text
Transaction
    Checking      -82.37

Split
    Groceries     -82.37
```

There is no category column on `transactions` to disagree with that row.

## A split transaction

Same tables, same rules, more rows. A `$150.00` purchase divided among Groceries `$90.00`,
Household `$40.00`, and Clothing `$20.00`:

```sql
INSERT INTO transactions (account_id, date, payee, memo, amount, status, check_number)
VALUES (1, '2026-09-02', 'Costco', '', -15000, 'uncleared', '');
-- RETURNING id -> 42

INSERT INTO splits (transaction_id, category_id, amount, memo)
VALUES (42, 10, -9000, ''),
       (42, 11, -4000, ''),
       (42, 12, -2000, '');
```

```text
Transaction
    Checking     -150.00

Splits
    Groceries     -90.00
    Household     -40.00
    Clothing      -20.00
                 -------
                 -150.00
```

The register shows *— Split —* in the category column rather than naming the first of the three,
because naming one would suggest the whole amount went there. `transaction.Entry` carries
`SplitCount` for exactly this, and `Entry.IsSplit()` is `SplitCount > 1`.

## Uncategorized transactions

A transaction may have no splits at all. That is a normal state, not an error: an imported bank
row whose category nobody has assigned yet is a transaction and nothing else.

```text
PANAMA JOE COFFEE    -18.42
```

So there are three shapes, and the count alone distinguishes them:

```text
0 splits     uncategorized
1 split      ordinary categorized transaction
2+ splits    split transaction
```

No synthetic `Uncategorized` category is created to satisfy the schema. The register prints
*Uncategorized* in the cell rather than leaving it blank, but that is a display decision in the
UI, not a row in `categories`.

**The nullable `category_id` is a second way to say the same thing**, and it is worth naming
rather than discovering. A split with `category_id IS NULL` is an amount assigned to no category
— `transaction.Split.CategoryID` is `0` for it, and `insert` binds NULL. The browser never
produces one today: `handleCreateTransaction` writes zero splits when no category is typed and
exactly one when a category is. The column stays nullable because a split *editor* needs it —
one line of a split can be filled in before its neighbour is, and refusing to store that would
mean either a placeholder category or a form that cannot be saved half-finished. Anything
reading splits must therefore handle NULL; `selectEntry` does it with
`COALESCE(c.name, '')` over a `LEFT JOIN`.

## The invariant, and where it is enforced

For a transaction that has any splits at all:

```text
transaction.amount == SUM(split.amount)
```

For the Costco example, `-15000 == -9000 + -4000 + -2000`. A split's sign follows its parent's,
which keeps the invariant a plain sum and keeps category aggregation from having to know which
direction the money went.

**Zero splits is uncategorized, not unbalanced** — `checkSplitTotal` in
`internal/transaction/transaction.go` returns nil for an empty slice, and it is the only place
the rule is stated. `Create` calls it *before* opening the write transaction, and the transaction
and its splits are then written inside one `sqlitex.ImmediateTransaction`, so a failure part way
through leaves no half-categorized entry behind. The lock is taken immediately rather than
upgraded partway, so a second writer fails to begin instead of failing after the splits are
already written.

**The invariant is enforced in the domain, not by a trigger or a CHECK.** SQLite cannot express
it in a constraint across two tables, and a trigger would put the rule somewhere the error
messages are worse and the tests are harder. The cost is that the rule holds only for writes that
go through `internal/transaction` — which is every write the program makes, and the reason SQL
issued by hand against a checkbook is not a supported way to edit one.

An audit query, should one ever be wanted:

```sql
SELECT t.id, t.amount, COUNT(s.id) AS splits, COALESCE(SUM(s.amount), 0) AS allocated
  FROM transactions t
  LEFT JOIN splits s ON s.transaction_id = t.id
 GROUP BY t.id, t.amount
HAVING splits > 0 AND allocated <> t.amount;
```

## Consequences

**What this buys.** One representation of a category, so reporting, editing, validation, and
import each have one code path. Spending by category needs no branch:

```sql
SELECT c.name, SUM(s.amount) AS amount
  FROM splits s
  JOIN transactions t ON t.id = s.transaction_id
  JOIN categories  c ON c.id = s.category_id
 WHERE t.date >= '2026-09-01' AND t.date < '2026-10-01'
 GROUP BY c.id, c.name
 ORDER BY c.name;
```

An ordinary transaction contributes one row to that and a split contributes several; the query
cannot tell them apart, which is the point.

**What it costs.** The common case pays for a second row and a join. At a household's scale —
a few thousand rows in an account, read whole by `LoadRegister` because paging would either
restart the running balance or need a second query to find its offset — that is not a cost worth
optimizing (TS-4). The register avoids the join twice over: `selectEntry` reads the split count
and the first category with two correlated subqueries rather than a `GROUP BY` that would then
have to be undone to get the count back.

**What follows from it.**

- **Editing a transaction rewrites its splits**, and `transaction.Update` does exactly that.
  Because the category is not a column, changing one is not an `UPDATE` of the parent row — it is
  a replacement of child rows, inside the same database transaction, guarded by the parent's
  `updated_at` (CO-3). `Edit.Splits` is a `*[]Split` so that nil can mean *leave them alone*,
  which is what lets the register's edit form change the payee of a transaction split three ways
  without flattening it; the invariant is still checked against whichever set will be stored, so
  changing that transaction's amount is refused rather than quietly breaking it.
- **Deleting a transaction takes its splits with it**, by cascade, with no extra statement — but
  only while `foreign_keys` stays on. `transaction.Delete` relies on it.
- **There is no `splits_category` index.** The draft proposed one. It has not been added because
  nothing reads splits by category yet; the reporting query above is the feature that would
  justify it, and it should arrive with that feature rather than in advance of it.
- **A split editor is the first thing that will produce a NULL `category_id`.** The paragraph
  above is a promise the schema is already keeping.

## Alternatives considered

**`category_id` on `transactions`, with child rows only for splits.** Rejected. It is smaller for
the common case and produces two places a category can live:

```text
ordinary transaction -> transactions.category_id
split transaction    -> splits.category_id
```

Every category-oriented operation then begins by asking which kind of transaction it has. The
join this decision pays for is cheaper than that branch, and unlike the branch it does not have
to be got right in each new feature.

**Double-entry postings.** Rejected. Splits are not postings and categories are not accounts: a
split explains part of one account's transaction, and nothing requires the set of them to balance
against anything but their own parent. Making categories into accounts would turn a checkbook into
a general ledger — a thing SC-3 does not list for version 1, and whose long-term maintenance cost
SC-1's third question does not justify for a household. SC-4 prefers omitting it to building half
of it.

## Vocabulary

The words the program uses, and which this ADR fixes. The user-facing definitions are in
[the glossary](../references/glossary.md); these are the model's.

| Term | Meaning |
| --- | --- |
| **Account** | A register whose balance changes as transactions are recorded. Owns the currency. |
| **Transaction** | A dated amount affecting exactly one account. Carries no category. |
| **Split** | An assignment of some or all of a transaction's amount to a category. |
| **Category** | A label classifying spending or income. A category is not an account. |
| **Transfer** | A relationship between two transactions in different accounts with equal and opposite amounts. Not built (RG-2, SC-3). |

The boundary that matters:

> Categories are not accounts, and splits are not double-entry postings.

The word *allocation* was used in the draft of this note and appears nowhere in the code, the
schema, or the interface. It is not an alternative name for a split; it is a term this project
does not use.
