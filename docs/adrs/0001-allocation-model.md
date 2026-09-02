# Transaction Allocations

## Purpose

Checkbook stores the effect of a transaction on an account separately from the way that transaction is categorized.

A transaction records what happened to an account:

- account
- date
- payee
- amount
- optional memo
- other register-oriented metadata

A transaction does **not** contain a category.

Categories are assigned through one or more allocation rows associated with the transaction.

This keeps the transaction model uniform: an ordinary categorized transaction has one allocation, while a split transaction has multiple allocations.

## Design principle

> The transaction records what happened to the account. The allocations explain what the transaction was for.

The category therefore belongs to an allocation, not to the parent transaction.

This avoids two possible representations for categorized transactions:

- category stored directly on an ordinary transaction
- category stored in child rows only for split transactions

Instead, every categorized transaction uses the same representation.

## Tables

A minimal SQLite schema might look like this:

```sql
CREATE TABLE transaction_record (
    id          TEXT PRIMARY KEY,
    account_id  INTEGER NOT NULL REFERENCES account(id),
    txn_date    TEXT NOT NULL,
    payee       TEXT,
    memo        TEXT,
    amount      INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE category (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE
);

CREATE TABLE allocation (
    id              TEXT PRIMARY KEY,
    transaction_id  TEXT NOT NULL
                    REFERENCES transaction_record(id)
                    ON DELETE CASCADE,
    category_id     INTEGER NOT NULL
                    REFERENCES category(id),
    amount          INTEGER NOT NULL,
    memo            TEXT
);

CREATE INDEX allocation_transaction
    ON allocation(transaction_id);

CREATE INDEX allocation_category
    ON allocation(category_id);
```

Money is stored as integer minor units, such as cents, rather than as `REAL` values.

For example, `$82.37` is stored as `8237` and a withdrawal of `$82.37` is stored as `-8237`.

## Ordinary categorized transaction

An ordinary transaction has exactly one allocation.

Suppose account `1` is Checking and category `10` is Groceries.

The account transaction is:

```sql
INSERT INTO transaction_record (
    id,
    account_id,
    txn_date,
    payee,
    memo,
    amount,
    created_at,
    updated_at
)
VALUES (
    'txn-001',
    1,
    '2026-09-02',
    'Supermercado Rey',
    NULL,
    -8237,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);
```

The category is represented by a single allocation:

```sql
INSERT INTO allocation (
    id,
    transaction_id,
    category_id,
    amount,
    memo
)
VALUES (
    'allocation-001',
    'txn-001',
    10,
    -8237,
    NULL
);
```

Conceptually:

```text
Transaction
    Checking      -82.37

Allocation
    Groceries     -82.37
```

There is no category column on `transaction_record`.

## Split transaction

A split transaction uses the same tables and the same rules. It simply has more than one allocation.

Suppose a `$150.00` purchase is divided among:

- Groceries: `$90.00`
- Household: `$40.00`
- Clothing: `$20.00`

The transaction is inserted once:

```sql
INSERT INTO transaction_record (
    id,
    account_id,
    txn_date,
    payee,
    memo,
    amount,
    created_at,
    updated_at
)
VALUES (
    'txn-002',
    1,
    '2026-09-02',
    'Costco',
    NULL,
    -15000,
    CURRENT_TIMESTAMP,
    CURRENT_TIMESTAMP
);
```

The category breakdown is represented by several allocations:

```sql
INSERT INTO allocation (
    id,
    transaction_id,
    category_id,
    amount,
    memo
)
VALUES
    ('allocation-002a', 'txn-002', 10, -9000, NULL),
    ('allocation-002b', 'txn-002', 11, -4000, NULL),
    ('allocation-002c', 'txn-002', 12, -2000, NULL);
```

Conceptually:

```text
Transaction
    Checking     -150.00

Allocations
    Groceries     -90.00
    Household     -40.00
    Clothing      -20.00
                 -------
                 -150.00
```

The application may describe this as a "split transaction," but there is no separate split-transaction type in the database.

The only distinction is the number of allocation rows.

## Allocation invariant

For a fully categorized transaction, the allocations must sum to the transaction amount:

```text
transaction.amount == SUM(allocation.amount)
```

For the Costco example:

```text
-15000 == -9000 + -4000 + -2000
```

The sign of an allocation follows the sign of the parent transaction. This keeps the invariant simple and makes category aggregation straightforward.

The invariant can be checked with SQL such as:

```sql
SELECT
    t.id,
    t.amount,
    COALESCE(SUM(a.amount), 0) AS allocated_amount
FROM transaction_record AS t
LEFT JOIN allocation AS a
    ON a.transaction_id = t.id
WHERE t.id = ?
GROUP BY t.id, t.amount;
```

The application should normally enforce this invariant while creating or editing a transaction, inside a database transaction.

For example:

```sql
BEGIN;

INSERT INTO transaction_record (...) VALUES (...);

INSERT INTO allocation (...) VALUES (...);
INSERT INTO allocation (...) VALUES (...);

-- Application verifies that the allocations sum to the transaction amount.

COMMIT;
```

A database trigger is possible, but is not required for the initial design.

## Uncategorized transactions

A transaction is allowed to have no allocations.

This is useful for imported bank transactions whose category has not yet been assigned.

For example:

```text
PANAMA JOE COFFEE    -18.42
```

can initially exist only in `transaction_record`.

This produces three natural states:

```text
0 allocations    uncategorized transaction
1 allocation     ordinary categorized transaction
2+ allocations   split transaction
```

There is therefore no need to create a synthetic `Uncategorized` category solely to satisfy the database schema.

## Category reporting

Because all categories are represented through allocations, reporting does not need separate logic for ordinary and split transactions.

For example, spending by category for September 2026 can be queried as:

```sql
SELECT
    c.name,
    SUM(a.amount) AS amount
FROM allocation AS a
JOIN transaction_record AS t
    ON t.id = a.transaction_id
JOIN category AS c
    ON c.id = a.category_id
WHERE t.txn_date >= '2026-09-01'
  AND t.txn_date <  '2026-10-01'
GROUP BY c.id, c.name
ORDER BY c.name;
```

An ordinary transaction contributes one allocation row. A split transaction contributes several. The reporting query treats both identically.

## Why not put `category_id` on the transaction?

Putting `category_id` directly on the transaction appears simpler for the common case, but creates two category representations:

```text
ordinary transaction -> transaction.category_id
split transaction    -> allocation.category_id
```

That complicates reporting, editing, validation, and the application model.

Every category-oriented operation must know whether the transaction is ordinary or split and inspect a different location accordingly.

With allocations, the common case costs one additional small row and a well-indexed join, but the data model becomes uniform:

```text
all categories -> allocation.category_id
```

For a personal checkbook-sized SQLite database, the cost of the additional join is negligible compared with the benefit of having one representation.

## Domain vocabulary

The database model supports the following vocabulary:

```text
Account
Transaction
Allocation
Category
Transfer
```

A useful definition for each is:

**Account**  
A register whose balance changes as transactions are recorded.

**Transaction**  
A dated amount affecting exactly one account.

**Allocation**  
An assignment of some or all of a transaction's amount to a category.

**Category**  
A label used to classify spending or income. A category is not an account.

**Transfer**  
A relationship between two account transactions with equal and opposite amounts.

The important boundary is:

> Categories are not accounts, and allocations are not double-entry postings.

The allocation model supports ordinary and split categorization without turning Checkbook into a general-ledger accounting system.

## Summary

The parent transaction table contains no category information.

Every category assignment is stored in `allocation`.

The resulting representation is:

```text
Transaction + 0 allocations     -> uncategorized
Transaction + 1 allocation      -> ordinary categorized transaction
Transaction + N allocations     -> split transaction
```

For categorized transactions:

```text
transaction.amount == SUM(allocation.amount)
```

This design accepts the small cost of always joining the allocation table in exchange for a simpler and more regular domain model.
