# User manual

Reference for `mmm`, the household checkbook. Applies to version **0.21.1-beta**.

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
- write a verified, timestamped backup into a `backups` folder beside the database
- list the backups it can find, and replace the checkbook with one of them in a single press,
  keeping the file it displaced
- close the checkbook, and open another — or the same one again, or a backup, or the sample
  household — without restarting
- open a database read-only, so a backup can be read without being altered
- restore a backup to a file of its own, bringing the copy up to date and leaving the backup as it
  is
- stop the program from the browser

It does not change an account once created — rename it, change its currency, close it, or remove
it — and it does not change or delete transactions already entered, split a transaction among
several categories, record a transfer between accounts, reconcile, import, export, search, or
produce reports. There is no terminal interface and no command-line subcommand.

## Starting the program

No packaged release exists. The program is run from a source checkout, from the repository root:

```sh
go run ./cmd/checkbook
```

It prints its version, the database in use, and the address to open, then serves until stopped:

```
checkbook 0.19.2-beta
database:  /Users/example/Documents/checkbook/checkbook.db
register:  http://127.0.0.1:8842/
press Ctrl+C to stop, or use Quit in the browser
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
| Default path | `checkbook.db` in the current directory, made absolute at startup — so what the status bar names is a file, not a name that depends on where the program was started |
| Created if missing | Yes |
| Directories created | Only `backups`, beside the checkbook, and only when **Back up now** is pressed, and the page says so. Opening a database creates none: a path whose parent directory does not exist is an error and nothing is written. |
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

The window has four regions: a title bar, the sidebar on the left, the register on the right, and
a status bar along the bottom. Both bars turn amber when the database is a temporary one and slate
when it is open read-only; see [A demo is marked as one](#a-demo-is-marked-as-one) and
[Opening a backup read-only](#opening-a-backup-read-only).

The sidebar has two sections: the accounts, and **This checkbook** — what can be done to the file
they are in.

### Account list

Every account in the database, open accounts first and alphabetically within each group. The
account being displayed is highlighted. A closed account is marked `closed`. Below the list,
**+ Add account** opens the form described next; the list is beside every page, so an account can
be added from anywhere, including from a database that has none. It is not shown when the database
is open read-only.

### This checkbook

Below the accounts, and beside every page:

| Action | Effect |
| --- | --- |
| **Back up now** | Writes a verified copy into the `backups` folder beside the database. See [Backing up](#backing-up). Not shown for `-demo` or a read-only database. |
| **Restore a backup** | Asks first, then replaces the checkbook with a backup. See [Restoring a backup](#restoring-a-backup). Not shown for `-demo`, for a read-only database, or for a build that cannot open a checkbook. |
| **Close checkbook** | Asks first, then closes the file. See [Closing and opening a checkbook](#closing-and-opening-a-checkbook). |

When the open database is a backup, **Restore a backup** is withheld — a backup is not a file this
program replaces — and a note under **Close checkbook** says so and points at the page you land on
after closing.

**Quit** is deliberately not here. It ends the program rather than acting on the checkbook, and it
lives on the page you land on after closing. See [Quitting](#quitting).

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

### A demo is marked as one

A database held in memory — what `-demo` serves — is marked in the frame of every page, so no
page can be mistaken for the register holding your records:

- the title bar and the status bar are amber rather than grey
- an hourglass with **Demo — nothing is saved** sits at the right of the title bar
- the status bar reads `Database: :memory:checkbook-demo — held in memory, nothing is saved`

The mark follows the database, not the `-demo` flag: any register whose database is held in
memory carries it — including the sample household opened from the browser rather than with the
flag. A register on a file never does.

## Backing up

**Back up now**, in the sidebar, writes a copy of the database into a `backups` folder beside it,
named `checkbook-YYYYMMDD-HHMMSS.db` from the local clock. The register is not interrupted and the
copy can be taken with the checkbook open.

```
~/Documents/checkbook/
    checkbook.db                            the checkbook
    backups/
        checkbook-20260902-141530.db        where Back up now writes
    checkbook-20260815-100000.db            an older backup: still found, still offered, not moved
    checkbook-replaced-20260902-153104.db   a checkbook a restore displaced
```

`backups` is the one directory this program creates. It is made on the first press if it is not
there, and the page says that it was. Backups written by earlier releases sit beside the checkbook
rather than inside it; they are not moved, and both places are read.

The copy is made with SQLite's `VACUUM INTO`, so it is a single compacted file with no `-wal`
beside it — routinely smaller than the database it came from. It is written under a working name
first, reopened read-only, checked with `PRAGMA quick_check`, and only then given a backup's name.
**A copy that will not read back is deleted rather than left looking like a backup**, and the page
says so.

**A backup is stamped as one.** SQLite databases carry an `application_id` in their header, and a
backup is given a different one from a checkbook — `MMM~` rather than `MMM `. The program will not
open a file carrying it for writing, so a backup cannot be migrated, typed into, or mistaken for
your records, however it is named or wherever it is moved to. It is the file itself that says what
it is; the timestamp in the name is for you, not for the program — which is why you can type a
backup's path into the open box without knowing it is one and get it opened for reading.

An existing backup is never written over. A second backup in the same second gets `-2` appended.

Afterwards the page you were on says which file was written. Copy it somewhere else — another
disk, or a service you already use — while you are thinking of it.

| Condition | Result |
| --- | --- |
| `-demo`, or any database held in memory | Refused. There is no file to copy, and the action is not offered. |
| The folder holding the database no longer exists | Refused. Only `backups` is ever created, and only inside a folder that is already there. |
| The `backups` folder cannot be created | Refused. No backup is written. |
| The copy will not reopen as a checkbook | Refused, the copy deleted, and the database reported as probably damaged. |

To restore one, see [Restoring a backup](#restoring-a-backup) below and
[How to restore a backup](../how-to/restore-a-backup.md).

## Closing and opening a checkbook

**Close checkbook** asks first, then lets go of the file. Nothing is changed or deleted, and the
program keeps running.

Closing is what makes the file safe to copy, move, or replace: with the checkbook closed there is
no `-wal` beside it waiting to be folded back in, so the single file is the whole checkbook.

Afterwards every window on the register — including ones you left open elsewhere — gets a page
saying no checkbook is open, with status **503**. That page offers:

- the name of the file that was closed, and **Back up now** for it — unless it was a backup, in
  which case the page says so and points at **Restore a backup** instead
- **Restore a backup**: the list of copies this program can find, and below it the two boxes of
  **Or restore to a file of its own**
- a box to open a checkbook by path, with **Open read-only — the file cannot be changed** under it
- **Open the sample household instead**
- **Quit**

Opening a path that does not exist creates a new, empty checkbook; a folder that does not exist is
reported, never created. A relative path is resolved against the directory the program was started
from. If the file will not open, the page says why in the same words the startup failure would use
and leaves nothing open.

**A close carries the checkbook it was drawn for.** If another window opened a different checkbook
in the meantime, the close is refused with status 409 rather than applied to a database the page
was never showing. Two windows closing the same checkbook is not a conflict: the second is told
what it wanted to hear.

## Opening a backup read-only

**A backup is opened read-only automatically.** Type its path into the open box and press
**Open**: the program reads the header, sees a backup, and opens it for reading. You do not have to
know the file is a backup, and you do not have to tick anything. Nothing is ever written to one.

**Open read-only — the file cannot be changed** is for the other case: a checkbook that could be
written to and is not going to be. Tick it to read your own records without any chance of changing
them.

Opening a database normally brings its schema up to date, so opening an older backup that way
would rewrite it — and a backup that has been rewritten is no longer the backup you took. A
read-only database is never migrated and never written to.

A read-only register is marked in the frame of every page: the title bar and status bar turn
slate, and a padlock sits at the right of the title bar — reading **Backup — nothing can be
changed** for a backup, and **Read-only — nothing can be changed** for a checkbook opened this way.
Every write action is withheld rather than offered and then refused — there is no entry form, no
status mark to press, no **+ Add account**, and no **Back up now**. A write that arrives anyway,
from a tab left open or an address typed in, is refused with status 409 and an explanation.

| Condition | Result |
| --- | --- |
| The file does not exist | Refused. Read-only never creates one. |
| The file is not a checkbook | Refused, unaltered. |
| The database is from an older release | Refused, unaltered, with instructions to restore it. |
| The database is from a newer release | Refused, unaltered. |

## Restoring a backup

Restoring acts on a **whole file**: you get the records that were in the backup, and the ones
entered since it was taken are not in them. It never merges two files, and it never alters the
backup it reads. Recovering *some* records from a backup would be an import rather than a restore,
and is not built; see [Restore and import](../explanations/restore-and-import.md).

There are two forms of it, and they are different operations.

### Replace this checkbook

**Restore a backup**, in the sidebar and on the page shown when no checkbook is open, lists every
copy this program can find, newest first:

| Column | What it shows |
| --- | --- |
| Taken | The moment in the file's name, when the name is one this program wrote; otherwise the file's own modification time, marked *file date*. |
| Size | The file's size. |
| File | Its path relative to the checkbook's folder, so a copy in `backups/` is visibly in `backups/`. A file that is a checkbook rather than a backup is marked as one. |

It reads two places — the checkbook's folder and the `backups` folder inside it — and creates
neither. **A file is a backup because its header says so**, never because of its name: a backup you
renamed `notes.txt` is listed, and a text file named `checkbook-20260101-000000.db` is not. The
checkbook itself is not listed, being the file that would be replaced. Files a previous restore set
aside — `checkbook-replaced-…` — are, since restoring one is how a restore is undone. The fifty
most recent files are offered.

Choosing one and pressing **Restore this backup** asks first (see below). Going through with it:

1. copies the backup to a new file **while the checkbook is still open and serving**, so a damaged
   backup, a full disk, or an unwritable folder is refused with the register still in front of you
   and nothing about it changed;
2. closes the checkbook, moves it aside as `checkbook-replaced-YYYYMMDD-HHMMSS.db` in the same
   folder — with its `-wal` and `-shm`, which belong to it — and moves the copy into its place;
3. opens the checkbook again and lands you on your register, with a notice naming the backup the
   records came from and the file that was kept.

The checkbook keeps its name. `checkbook.db` is still `checkbook.db`, so every bookmark, shortcut
and `-db` flag still points at it.

**Nothing is deleted.** The file that was displaced is an ordinary checkbook, not a backup, so it
opens directly — which is a shorter way back than a file that must itself be restored.

**The list is served whether or not a checkbook is open**, including when the checkbook the program
was started on could not be opened at all. That is the case this exists for.

| Condition | Result |
| --- | --- |
| The chosen file is not one the list just offered | Refused. Nothing is done. |
| The page was drawn for a different checkbook from the one open now | Refused with status 409. Nothing is replaced. |
| The backup is not a checkbook or a backup of one | Refused. The checkbook is still open and unchanged. |
| The backup is from a newer release | Refused. The checkbook is still open and unchanged. |
| The copy will not open as a checkbook | Refused, the copy deleted, the backup reported as probably damaged, and the checkbook still open and unchanged. |
| The checkbook cannot be moved aside — another program has it open | Refused. Nothing is replaced, and the copy is left where the page names it. |
| The copy cannot take the checkbook's name | The checkbook is put back exactly as it was. The copy is left, and the page names it. |
| Neither can be put in place | Reported on its own, naming both files. Nothing is deleted; you rename one of them yourself. |
| The restored checkbook will not open | The swap stands. The page says why and names the file that was kept, so you can open that instead. |
| `-demo`, a read-only database, or a build with no way to open a checkbook | Withheld, with the reason on the page. |

### Or restore to a file of its own

Below the list, two boxes: the backup to restore **from**, and the file to restore **to**. The
second must not exist yet. This copies the backup to that name and **replaces nothing**; you open
the copy yourself.

Use it when the file you want back is not the one you are working in, or when the backup is on
another disk — the list reads only the checkbook's own folder.

| Condition | Result |
| --- | --- |
| The file to restore to already exists | Refused. Nothing is written, and a free name is suggested. |
| The folder to restore into does not exist | Refused. No folder is created. |
| The file to restore from is not there | Refused. |
| The file to restore from is not a checkbook or a backup of one | Refused, unaltered. |
| The backup is from a newer release | Refused, unaltered, with instructions to use that release. |
| The copy will not open as a checkbook | Refused, the copy deleted, and the backup reported as probably damaged. |

A checkbook is accepted where a backup is, so this is also how you copy your records to a second
file without leaving the browser.

### Asking first

Both the one-press restore and **Close checkbook** ask before acting, because both take the
register away from every window the household has open. The confirmation names the file the
records will come from, the file they will replace, and says that anything entered since the backup
was taken is not in it. **Keep what I have** leaves everything as it is.

The confirmation carries the checkbook it was drawn for. If another window closed or opened a
checkbook in the meantime, pressing it is refused with status 409 rather than applied to a database
the page was never showing.

### What migration has to do with it

Restoring is the **only** place this program brings an old database's schema up to date on a file
that was not just opened for use. Opening a backup read-only refuses an older schema outright,
because bringing it up to date would rewrite it and a backup that has been rewritten is no longer
the backup you took. Restoring has no such problem: it migrates the *copy*, and leaves the backup
alone. So a backup from any release this program can still migrate from is restorable, however old.

## Quitting

**Quit**, on the page you land on after closing, ends the program. It asks first.

The confirmation names the database that is open. Going through with it renders a page saying the
program has stopped, along with the command that starts it again and the directory to run it from,
and then stops: in-flight requests are allowed to finish, the checkbook is closed, and its `-wal`
and `-shm` files are removed. The window will not answer afterwards.

The goodbye page carries its own styling and links nowhere, because by the time your browser could
ask for anything else the program is gone.

Ctrl+C in the terminal does exactly the same thing by the same path.

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
| `/backup` | `POST` only. Writes a verified backup and redirects back to the page it was pressed from. Works with no checkbook open, on the file just closed. |
| `/checkbook` | The page shown when no checkbook is open. Redirects to `/` when one is. |
| `/checkbook/close` | `GET` asks; `POST` closes. |
| `/checkbook/open` | `POST` only. Opens a checkbook by path, read-only if asked, or the sample household. Answers with a redirect to the register. |
| `/checkbook/restore` | `GET` lists the backups found. `POST` replaces the checkbook with one and reopens it, then redirects to the register. The `POST` is not served by a build that cannot open a checkbook. |
| `/checkbook/restore/confirm` | `GET` only. Asks before replacing, carrying the checkbook the page was drawn for. |
| `/checkbook/restore/copy` | `POST` only. Restores a backup to a new file and redirects back with its name. Replaces nothing and opens nothing. |
| `/quit` | `GET` asks; `POST` stops the program. |
| `/static/app.css` | The stylesheet. |
| `/static/htmx.min.js` | The script that replaces a single row. Served from this program; nothing is fetched from the internet. |

`/backup`, `/checkbook/close`, `/checkbook/open`, `/checkbook/restore`, `/checkbook/restore/copy`
and `/quit` act on your file or on the program
rather than on a record, so they accept `POST` only and are refused unless the request came from a
page this program served. There is no login, no session, and no token: the check is the two headers
(`Sec-Fetch-Site` and `Origin`) a browser fills in by itself, so a form on another site cannot stop
your program or close your checkbook. Requests with neither header — `curl`, a script — are
unaffected.

Any other address returns a page reporting that the address is not served. A method an address
does not accept — anything but `GET` on a register, anything but `POST` on the entry address — is
refused with status 405.

Accounts may be opened in several browser tabs at once, and more than one copy of the program may
run at the same time, including on the same database, provided each is given its own `-port`. SQLite coordinates their access to the file.
Each window shows the register as of when it was loaded and does not refresh when another writes;
a write that would overwrite a change made since the page was loaded is refused rather than
applied silently.

## When the database cannot be opened

The program does not exit. It runs as it always does, with no checkbook open, so every address that
does not need one still answers — most importantly **Restore a backup**, which is why the program
is still running at all. Any address that needs a checkbook, a bookmarked register included, gets
the page shown when none is open, and that page carries the explanation: what the database was,
what happened, what to do next, links to the relevant documents, the underlying message, and the
list of backups sitting beside the file that would not open.

Those responses are status **503**. `/static/app.css` is served normally, so the page is legible.
Once anything opens successfully the explanation stops being shown: it is no longer what is wrong.

The failure is also printed to the terminal, and the startup banner reads `NOT OPENED` in place
of the usual database line. When the program is stopped it exits with status 1.

| Condition | Page heading |
| --- | --- |
| The database was written by a newer version of the program | This checkbook was written by a newer version of the program |
| The file is a SQLite database this program did not create | That file is not a checkbook |
| The directory named by `-db` does not exist | That folder does not exist |
| The schema is behind the version this build expects | This checkbook's schema is not the one this version expects |
| The file has bytes and is not a SQLite database at all | That file is not a checkbook |
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
| No checkbook is open | 503, on every address that needs one; `/checkbook`, `/checkbook/restore` and the static files still answer |
| A checkbook could not be opened | 422, on the page that offers to open one |
| A close arrives for a checkbook that is no longer the one open | 409 |
| A write arrives for a database open read-only | 409 |
| A backup is refused because there is no file to copy | 409 |
| A backup is named in the open box | Opened read-only, 303 to the register |
| A restore is refused because the file was not one the list offered | 422 |
| A restore is refused because the page was drawn for a different checkbook | 409 |
| A restore is refused because the checkbook is the demo, read-only, or has no file behind it | 409 |
| A restore was made and the files could not be moved into place | 409, or 500 when neither file is at the checkbook's name |
| A control request did not come from a page this program served | 403 |

Each of these pages states what happened and what to do next, and links back to the register.

## Stopping the program

Use **Quit** in the browser (see [Quitting](#quitting)), press **Ctrl+C** in the terminal, or send
the process `SIGTERM`. All three take the same path: in-flight requests are allowed to finish, the
database is closed, and the `-wal` and `-shm` files are removed.

Closing the browser window does not stop the program. Neither does **Close checkbook**, which lets
go of the file and leaves the program running.
