# User manual

Reference for `mmm`, the household checkbook. Applies to version **0.6.0-beta**.

This page describes the program as it is. For the reasoning behind it, see
[About mmm](../explanations/what-is-mmm.md). To set up a database of your own, see
[How to create your first checkbook](../how-to/create-your-first-checkbook.md); to move to a newer
version, see [How to upgrade the application](../how-to/upgrade-the-application.md).

## What this release does

The program displays a check register from a local database file. It is **read-only**.

It shows:

- the accounts in the database
- one account's transactions, in date order, with a running balance
- the ending balance, the cleared balance, and the difference between them
- the path of the database file in use

It does not create or edit accounts, enter, change, or delete transactions, mark transactions
cleared, reconcile, import, export, create backups, search, or produce reports. There is no
terminal interface and no command-line subcommand.

## Starting the program

No packaged release exists. The program is run from a source checkout, from the repository root:

```sh
go run ./cmd/checkbook
```

It prints its version, the database in use, and the address to open, then serves until stopped:

```
checkbook 0.6.0-beta
database:  /Users/example/Documents/checkbook/checkbook.db
register:  http://127.0.0.1:53219/
press Ctrl+C to stop
```

## Options

| Option | Default | Description |
| --- | --- | --- |
| `-db PATH` | `checkbook.db` | Database file to open. A relative path is resolved against the current directory. |
| `-host ADDR` | `127.0.0.1` | Address to listen on. Only `127.0.0.1`, `::1`, other loopback addresses, and the name `localhost` are accepted. |
| `-port N` | `0` | Port to listen on. `0` asks the operating system for a free port, which is printed at startup. |
| `-open` | `true` | Open the register in the default browser at startup. Use `-open=false` to suppress. |
| `-demo` | `false` | Serve a sample household held in memory. No file is read or written. |
| `-version` | — | Print the version and exit. |

Options are Go flags: `-db path` and `-db=path` are both accepted; boolean options must be
written `-open=false` to turn them off.

## The database file

All records live in one SQLite file.

| Property | Value |
| --- | --- |
| Default path | `checkbook.db` in the current directory |
| Created if missing | Yes |
| Directories created | Never. A path whose parent directory does not exist is an error and nothing is written. |
| Companion files while running | `NAME-wal` and `NAME-shm`, in the same directory |
| Companion files after a clean stop | None. Their contents are folded into the database file. |
| Format | SQLite 3, readable by any SQLite tool |

The file carries an application identifier of `0x4d4d4d20`. A SQLite file that does not carry it
is refused, so the program cannot write its schema into an unrelated database.

The schema is brought up to date when the file is opened, and is only ever applied forwards. The
schema version is checked against the version this build expects:

| Database schema | Result |
| --- | --- |
| Behind this build | Brought up to date, then opened |
| Equal to this build | Opened |
| Ahead of this build | Refused. The program does not open a database written by a newer release. |

A database is never migrated backwards.

`-demo` uses a database held in memory. It is reported as `:memory:checkbook-demo` and is
discarded when the program stops.

## The register screen

The window has four regions: a title bar, the account list on the left, the register on the
right, and a status bar along the bottom.

### Account list

Every account in the database, open accounts first and alphabetically within each group. The
account being displayed is highlighted. A closed account is marked `closed`.

### Account heading

The account name, followed by its type and currency, for example `checking · USD`.

Account types are `checking`, `savings`, `credit`, and `cash`. Currency is a property of the
account; every amount in a register is in that account's currency.

### Register columns

Transactions are listed oldest first, ordered by date and then by the order in which they were
recorded.

| Column | Contents |
| --- | --- |
| Date | The transaction's calendar date, `YYYY-MM-DD` |
| Num | Check number, blank if none |
| Payee | Payee text as recorded |
| Category | See below |
| Memo | Memo text as recorded |
| Amount | Signed amount; negative amounts are shown in red |
| ✓ | Status; see below |
| Balance | Account balance through this row, inclusive |

### Category column

| Shown | Meaning |
| --- | --- |
| A category name | The transaction is assigned to one category |
| `— Split —` | The transaction is divided among more than one category |
| `Uncategorized` (grey) | The transaction has no category |

A split transaction names no category. The individual parts of a split are not displayed in this
release.

### Status column

| Mark | Status | Meaning |
| --- | --- | --- |
| blank | uncleared | Recorded in the checkbook only |
| `c` | cleared | The institution has shown the transaction |
| `R` | reconciled | Recorded by a completed reconciliation |

Cleared and reconciled are separate facts and are not combined.

### Amounts

Amounts are exact. They are held as whole minor units of the account's currency — cents for USD —
and are never stored or calculated as binary floating-point numbers.

They are displayed with the number of decimal places the currency uses (2 for USD and EUR, 0 for
JPY, 3 for KWD) and with the integer part grouped in threes: `-4,817.29`.

### Dates

A transaction date is a calendar date. It is displayed exactly as recorded and is never shifted
into or out of a time zone.

### Totals

Shown below the register, in the account's currency.

| Total | Definition |
| --- | --- |
| Ending balance | The account's opening balance plus every transaction in the register |
| Cleared balance | The opening balance plus the transactions marked `c` or `R` |
| Not yet cleared | Ending balance minus cleared balance, with the number of uncleared transactions |

An account with no transactions shows its opening balance as both the ending and cleared balance.

### Status bar

The path of the database file in use, and the program's version.

## Addresses

| Address | Response |
| --- | --- |
| `/` | Redirects to the first account. If the database has no accounts, a page saying so. |
| `/accounts/N` | The register for account `N`. Stable: an account's number is never reassigned. |
| `/static/app.css` | The stylesheet. |

Any other address returns a page reporting that the address is not served. Any request that is
not a `GET` is refused with status 405.

Accounts may be opened in several browser tabs at once.

## When the database cannot be opened

The program does not exit. It serves one page, at **every** address, reporting the problem, and
opens the browser on it as usual. The page names the database, states what to do next, links to
the relevant documents, and shows the underlying message.

Every response is status **503** while the program is in this state, except `/static/app.css`,
which is served normally so the page is legible. The register is not served at all; a bookmarked
account address returns the same page.

The failure is also printed to the terminal, and the startup banner reads `NOT OPENED` in place
of the usual database line. When the program is stopped it exits with status 1.

| Condition | Page heading |
| --- | --- |
| The database was written by a newer version of the program | This checkbook was written by a newer version of the program |
| The file is a SQLite database this program did not create | That file is not a checkbook |
| The directory named by `-db` does not exist | That folder does not exist |
| The schema is behind the version this build expects | This checkbook's schema is not the one this version expects |
| Any other failure to open the file | The checkbook could not be opened |

In every case the database is left as it was found.

## Messages at startup

The program exits with status 1, without opening the database or the listener, when it prints
either of the following. Neither can be reported in the browser, because there is no listener.

| Message | Condition |
| --- | --- |
| `-host X is not a loopback address; the register is served only to this machine` | `-host` names an address reachable from another machine. |
| `-host "X" is not a loopback address; use 127.0.0.1, ::1, or localhost` | `-host` is a name other than `localhost`. |
| `listen on ADDR: ... bind: address already in use` | Another program, or another copy of this one, holds the port. |

A browser that cannot be opened is reported as a warning; the program continues, and the address
is on screen.

## Pages shown for errors

| Condition | Status |
| --- | --- |
| The address does not name an account, for example `/accounts/nope` | 404 |
| The account number does not exist in this database | 404 |
| The address is not served | 404 |
| The database cannot be read | 500 |

Each of these pages states what happened and what to do next, and links back to the register.

## Stopping the program

Press **Ctrl+C** in the terminal, or send the process `SIGTERM`. In-flight requests are allowed to
finish, the database is closed, and the `-wal` and `-shm` files are removed. Closing the browser
window does not stop the program.
