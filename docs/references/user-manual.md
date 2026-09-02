# User manual

Reference for `mmm`, the household checkbook. Applies to version **0.14.0-beta**.

This page describes the program as it is. For the reasoning behind it, see
[About mmm](../explanations/what-is-mmm.md). To set up a database of your own, see
[How to create your first checkbook](../how-to/create-your-first-checkbook.md); to move to a newer
version, see [How to upgrade the application](../how-to/upgrade-the-application.md).

## What this release does

The program displays a check register from a local database file, and accepts accounts and
transactions into it.

It shows:

- the accounts in the database
- one account's transactions, in date order, with a running balance
- the ending balance, the cleared balance, and the difference between them
- the path of the database file in use

It can:

- create an account, with a kind, a currency, and an opening balance
- enter a transaction into an account, with an optional category
- mark a transaction cleared, and mark it not cleared again

It does not change an account once created — rename it, change its currency, close it, or remove
it — and it does not change or delete transactions already entered, split a transaction among
several categories, record a transfer between accounts, reconcile, import, export, create backups,
search, or produce reports. There is no terminal interface and no command-line subcommand.

## Starting the program

No packaged release exists. The program is run from a source checkout, from the repository root:

```sh
go run ./cmd/checkbook
```

It prints its version, the database in use, and the address to open, then serves until stopped:

```
checkbook 0.14.0-beta
database:  /Users/example/Documents/checkbook/checkbook.db
register:  http://127.0.0.1:8842/
press Ctrl+C to stop
```

## Options

| Option | Default | Description |
| --- | --- | --- |
| `-db PATH` | `checkbook.db` | Database file to open. A relative path is resolved against the current directory. |
| `-host ADDR` | `127.0.0.1` | Address to listen on. Only `127.0.0.1`, `::1`, other loopback addresses, and the name `localhost` are accepted. |
| `-port N` | `8842`, or `8843` with `-demo` | Port to listen on. `0` asks the operating system for a free port instead, which is printed at startup. A port given here is used whatever else is on the command line. |
| `-open` | `true` | Open the register in the default browser at startup. Use `-open=false` to suppress. |
| `-demo` | `false` | Serve a sample household held in memory. No file is read or written, and it listens on `8843` so it can run beside your own register. |
| `-version` | — | Print the version and exit. |

Options are Go flags: `-db path` and `-db=path` are both accepted; boolean options must be
written `-open=false` to turn them off.

## The address

The register is served at `http://127.0.0.1:8842/` unless `-host` or `-port` says otherwise. The
port is fixed rather than chosen by the system, so the address is the same every time the program
runs and can be bookmarked.

`-demo` listens on `http://127.0.0.1:8843/` instead, so the sample household can be opened while
your own register is running. That port is fixed for the same reason. Giving `-port` overrides
either default, including `-port 8842` alongside `-demo`.

Only one program can hold a port. Starting a second copy on the same port does not start a second
register; it reports the address and exits with status 1:

```
checkbook: port 8842 is already in use

If your checkbook is already open, this is where it is:
    http://127.0.0.1:8842/

Open that address, or close the copy that is running before starting
another. If something else on this machine is using port 8842, start
this copy on a different one:
    -port 8844
```

The suggested port skips `8843`, since that is where a demo would be.

The program does not check what is listening, and does not claim to know. To run a second
checkbook on another database at the same time, give it its own port with `-port`.

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

`-demo` uses a database held in memory. It is reported as `:memory:checkbook-demo`, is discarded
when the program stops, and is served on its own port so it never has to displace the register
holding your records.

## The register screen

The window has four regions: a title bar, the account list on the left, the register on the
right, and a status bar along the bottom.

### Account list

Every account in the database, open accounts first and alphabetically within each group. The
account being displayed is highlighted. A closed account is marked `closed`. Below the list,
**+ Add account** opens the form described next; the list is beside every page, so an account can
be added from anywhere, including from a database that has none.

### Adding an account

`/accounts/new` is a form of four fields.

| Field | Required | Notes |
| --- | --- | --- |
| Name | Yes | Must not already be in use. Names are compared without regard to case, so `checking` and `Checking` are the same account. |
| Kind | Yes | `Checking`, `Savings`, `Credit card`, or `Cash`. Stored as `checking`, `savings`, `credit`, `cash`. |
| Currency | Yes | Every currency the build knows: USD, EUR, GBP, JPY, KWD. On an empty database the box opens on USD; once there is an account, it opens on that account's currency. |
| Opening balance | No | The balance before the first transaction entered. Empty means zero. |

Unlike the register's Payment and Deposit boxes, the opening balance is **one box and takes a
sign**: a card already owed on opens negative, and there is nothing else here to say so. An amount
more precise than the currency — a third decimal place in USD — is refused rather than rounded.

On success the browser goes to the new account's register, where the opening balance is the
starting balance and, having no transactions, the ending and cleared balances both. Reloading that
page does not create a second account.

If the account is refused, the form comes back with what was typed still in it and a message above
it saying what was wrong and what to do about it. Nothing is written.

**Nothing in this release changes an account after it is created.** The name, the kind, the
currency, and the opening balance are fixed at creation, and an account cannot be closed or
removed from the browser.

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

### Marking a transaction cleared

The mark in the status column is a button. Pressing it on an uncleared transaction marks it
cleared; pressing it on a cleared transaction marks it uncleared again. A dot stands in for the
empty mark, so there is something to aim at.

Only that row and the totals change: the page is not reloaded and the position on it is kept.
Marking a transaction cleared moves the cleared balance and the count of what is not yet cleared.
It does not move the ending balance, which counts every transaction whatever its status.

**A reconciled transaction has no button.** Reconciled is recorded by a completed reconciliation,
and the register does not rewrite it.

If the same account is open in more than one window, a mark made against a transaction that has
since changed elsewhere is **refused, not applied**. The row is replaced with the transaction as
it now stands and a message says what happened, so the two windows cannot silently overwrite each
other. Mark it again if that is still what you want.

The mark works without JavaScript. Each one is an ordinary form: with scripting available only the
row is replaced, and without it the same press reloads the register.

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

### Entering a transaction

Below the totals is a form that adds one transaction to the account on screen. A closed account
does not accept entries.

| Field | Required | Notes |
| --- | --- | --- |
| Date | Yes | A calendar date, `YYYY-MM-DD`. Defaults to today. |
| Num | No | Check number. |
| Payee | Yes | |
| Category | No | Blank records no category, and the row reads `Uncategorized`. The categories already in use are offered as suggestions. |
| Memo | No | |
| Payment | One of the two | Money leaving the account. |
| Deposit | One of the two | Money arriving in the account. |

**Amounts are typed without a sign.** The column decides the direction: an amount under Payment is
recorded as negative, and one under Deposit as positive. Filling both, or neither, is refused. An
amount more precise than the account's currency — a third decimal place in USD — is refused rather
than rounded.

The category box lists the categories already in use, in name order, and the browser narrows the
list as you type. The list suggests without restricting: any name can still be typed, and one that
does not exist yet is created.

A category name that already exists is reused, regardless of case: typing `groceries` when
`Groceries` exists files the transaction under the existing category and does not create a second
one. The stored spelling is not changed.

A new transaction is always **uncleared**; the bank showing it is a separate fact, recorded
separately (see [Marking a transaction cleared](#marking-a-transaction-cleared)).

On success the browser returns to the register, positioned on the new row, and the running
balances and totals are recalculated. Reloading that page does not enter the transaction a second
time.

If the entry is refused, the register comes back with the entry still in the form and a message
above it saying what was wrong and what to do about it. Nothing is written. The transaction and
its category are written together or not at all.

### Status bar

The path of the database file in use, and the program's version. Beside the version is a
GitHub mark linking to the project's issue tracker; following it opens a browser tab to
GitHub. Nothing on the page is fetched from the network, and the program itself never
contacts GitHub.

## Addresses

| Address | Response |
| --- | --- |
| `/` | Redirects to the first account. If the database has no accounts, a page saying so. |
| `/accounts/new` | The form for a new account. More specific than `/accounts/N`, so no account is ever reached at this address. |
| `POST /accounts` | Creates an account. Answers with a redirect to its register. |
| `/accounts/N` | The register for account `N`. Stable: an account's number is never reassigned. |
| `POST /accounts/N/transactions` | Enters a transaction into account `N`. Answers with a redirect back to the register. |
| `POST /accounts/N/transactions/M/status` | Marks transaction `M` cleared or uncleared. Answers with the row and the totals, or with a redirect when the request did not come from the page's script. |
| `/static/app.css` | The stylesheet. |
| `/static/htmx.min.js` | The script that replaces a single row. Served from this program; nothing is fetched from the internet. |

Any other address returns a page reporting that the address is not served. A method an address
does not accept — anything but `GET` on a register, anything but `POST` on the entry address — is
refused with status 405.

Accounts may be opened in several browser tabs at once, and more than one copy of the program may
run at the same time, including on the same database, provided each is given its own `-port`. SQLite coordinates their access to the file.
Each window shows the register as of when it was loaded and does not refresh when another writes;
a write that would overwrite a change made since the page was loaded is refused rather than
applied silently.

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
any of the following. None of them can be reported in the browser, because there is no listener.

These are the only failures with nowhere else to report themselves: they happen before there is a
listener, so there is no page and no browser window to carry them. On Windows, when the program
was started from the shell — its icon double-clicked, or opened from the Start menu, the taskbar,
or a shortcut — the window is held open with `Press Enter to close this window.` so the message
can be read. Started from a command line it is not: you keep your window, and your prompt comes
back — a console allocated by double-clicking the program otherwise
closes the instant it exits, taking the message with it. A failure that *is* shown in the browser,
such as a database that will not open, does not hold the console.

| Message | Condition |
| --- | --- |
| `-host X is not a loopback address; the register is served only to this machine` | `-host` names an address reachable from another machine. |
| `-host "X" is not a loopback address; use 127.0.0.1, ::1, or localhost` | `-host` is a name other than `localhost`. |
| `port N is already in use` | Something already holds the port. See [The address](#the-address). |
| `listen on ADDR: ...` | The address could not be listened on for some other reason. |

A browser that cannot be opened is reported as a warning; the program continues, and the address
is on screen.

## Pages shown for errors

| Condition | Status |
| --- | --- |
| The address does not name an account, for example `/accounts/nope` | 404 |
| The account number does not exist in this database | 404 |
| The address is not served | 404 |
| The database cannot be read | 500 |
| An entry is refused; the register returns with the entry still in the form | 422 |
| An account is refused, including a name already in use; the form returns with what was typed still in it | 422 |
| An entry is addressed to a closed account | 409 |
| A mark arrives without the version of the transaction it was made against | 400 |
| A mark is refused because the transaction changed in another window | 409, or the row and a message when the page's script made the request |

Each of these pages states what happened and what to do next, and links back to the register.

## Stopping the program

Press **Ctrl+C** in the terminal, or send the process `SIGTERM`. In-flight requests are allowed to
finish, the database is closed, and the `-wal` and `-shm` files are removed. Closing the browser
window does not stop the program.
