# Project Structure

## There is also an intriguing hybrid

I'd strongly consider separating the program into:

```text
internal/
    account
    transaction
    reconciliation
    import
    storage

cmd/
    checkbook/
    checkbook-tui/
```

Where `checkbook` is the friendly browser UI and `checkbook-tui` is your interface.

Both operate on exactly the same SQLite database and domain packages.

Then you can fool around with the TUI without imposing it on anyone else.

You could even eventually have:

```text
checkbook import statement.qfx
checkbook export --ledger
checkbook backup
```
