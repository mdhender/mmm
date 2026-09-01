# Sqlite3 Data Store

I wouldn't make Ledger text the primary database.

This is where I'd depart a little from the Ledger inspiration.

You _could_ have:

```text
2026-08-29 * Felipe Motta
    Expenses:Wine                $36.42
    Assets:Checking             -$36.42
```

as your canonical storage.

There's an elegance to that.

But reconciliation starts complicating things.

So do:

* splits
* transaction IDs
* imported transactions
* duplicate detection
* recurring transactions
* notes
* cleared vs reconciled
* reconciliation sessions
* corrections

You end up inventing metadata conventions inside your "simple" text format.

SQLite already solves all of that extremely well.

I'd instead make the model:

```text
checkbook.db        ← canonical

checkbook.ledger    ← export
checking.csv        ← export
backup/
```

And perhaps support Ledger-format import/export from the beginning.

That gives you the most important property of Ledger: your financial information is never trapped inside the program.

That's more important to me than whether the source of truth itself is text.

----

## The data model could remain tiny

Something approximately like this:

```text
accounts
    id
    name
    type
    opening_balance
    closed_at

transactions
    id
    account_id
    date
    payee
    memo
    amount
    status
    check_number

splits
    id
    transaction_id
    category_id
    amount
    memo

categories
    id
    name

reconciliations
    id
    account_id
    statement_date
    statement_balance
```

Maybe scheduled transactions later.

That's nearly the entire application.

Notice what's conspicuously absent:

* bank_credentials
* cloud_sync
* investment_quotes
* tax_planning
* credit_scores
* advertising
* online_bill_pay
* AI categorization
* subscription_manager
* financial_advisor

Good.
