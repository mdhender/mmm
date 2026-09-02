# The Checkbook Specification

This document is the binding one. Every requirement here is meant to be checkable against a
change: a diff either satisfies it or it does not.

`MANIFESTO.md` explains *why* these rules exist and what the program is for. It is worth reading,
and it settles questions of taste and direction — but when something needs to be adjudicated,
adjudicate it here.

**MUST** and **MUST NOT** are absolute. **SHOULD** marks a strong default that may be traded away
for a stated reason. Requirements are numbered so a review, issue, or commit message can cite one
(`violates IE-6`).

Requirements derive from `MANIFESTO.md`. The notes under `docs/` are design intent, not
requirements; where they conflict with this document, this document wins.

## PL — Platform and deployment

- **PL-1** Windows and macOS are equal targets. A release MUST be an ordinary native executable
  for each.
- **PL-2** The application MUST run without a database server, Node.js, a browser extension, an
  installer framework, or a permanent background service.
- **PL-3** The application MUST NOT require a vendor account, a cloud service, or an internet
  connection in order to run.
- **PL-4** The web UI MUST bind only to the loopback interface, and MUST open in the user's normal
  browser rather than shipping one.
- **PL-5** The same database format and behavior MUST work on both platforms. Moving the household
  records to a new computer MUST require only copying files — never deactivating one machine,
  authorizing another, or contacting support.
- **PL-6** A release MUST state where the data lives and how to back it up.
- **PL-7** The web UI serves one household over loopback, with no remote origin and no accounts,
  so it MUST NOT grow authentication, authorization, session management, or CSRF machinery. There
  is no second principal for such a mechanism to distinguish. Anything that reintroduces one is an
  SC-1 question, not an implementation detail.

## ST — Data and storage

- **ST-1** Canonical records MUST live in a local SQLite database.
- **ST-2** The database MUST be an ordinary file that can be copied, backed up, and restored
  without asking anyone's permission.
- **ST-3** If development stops, the records MUST remain readable and recoverable with ordinary
  tools.
- **ST-4** Database migrations MUST be explicit. Schema MUST NOT be created or altered as a side
  effect of ordinary startup.
- **ST-5** The data model SHOULD stay close to `accounts`, `transactions`, `splits`, `categories`,
  and `reconciliations`. Growth beyond that needs an SC-1 justification.
- **ST-6** Opening a database MUST create the file but MUST NOT create directories. A path whose
  parent directory does not exist MUST fail, and MUST NOT leave anything behind. A mistyped path
  is a mistake to report, not a tree to build.
- **ST-7** An instant MUST be stored in UTC, as `YYYY-MM-DDTHH:MM:SS.ffffffZ`. One timezone and a
  fixed width are what let a text column sort chronologically and answer range queries. A local
  time, or a mixture of offsets, MUST NOT be stored.
- **ST-8** A calendar date — a transaction's date, a statement date — is not an instant. It MUST
  remain timezone-free and MUST NOT be converted to or from UTC: a purchase made on the 29th is
  dated the 29th regardless of where the household reads it.
- **ST-9** Primary keys MUST NOT be reused. Deleting a record MUST NOT free its identifier for a
  later one, or anything still holding that identifier — an open tab, a bookmark, an export, a
  reconciliation — would silently come to mean a different record.

## RG — Register and core operations

- **RG-1** The primary screen MUST be a check register showing date, payee, category, memo,
  amount, status, and running balance.
- **RG-2** The application MUST support, as first-class operations: create and edit accounts;
  enter, change, and remove transactions; split a transaction among categories; record transfers
  between accounts; mark transactions cleared; reconcile an account against a statement; search
  and filter the register; import transactions from files; export records and create backups.
- **RG-3** Destructive actions MUST require confirmation.
- **RG-4** Errors MUST say what happened and what the user can safely do next.
- **RG-5** Instants MUST be displayed in the timezone the person reading them is in, converted at
  the point of display. The server stores UTC (ST-7) and MUST NOT assume a household timezone.
  Calendar dates (ST-8) are shown as written and MUST NOT be shifted.

## RC — Reconciliation

- **RC-1** A reconciliation MUST always show the statement balance, the cleared balance, and the
  remaining difference.
- **RC-2** Discrepancies MUST be visible. The application MUST NOT explain them away or
  manufacture agreement.
- **RC-3** Finishing a reconciliation records a fact. It MUST NOT rewrite history, insert
  adjustment transactions, or silently change prior transactions.

## IE — Import and export

- **IE-1** The application MUST import QIF, OFX/QFX, and CSV files.
- **IE-2** The application MUST NOT connect directly to financial institutions.
- **IE-3** The application MUST NOT store bank credentials.
- **IE-4** The application MUST NOT depend on a third-party aggregation service.
- **IE-5** Before committing an import, the application MUST show what it found, identify possible
  duplicates, and let the user review the result.
- **IE-6** Imports MUST be repeatable without quietly creating duplicates.
- **IE-7** The application MUST NOT silently download, categorize, merge, or delete transactions.
  It MAY remember useful choices and suggest likely categories; the final record is always the
  user's decision.
- **IE-8** The application MUST export documented open formats, at least CSV and Ledger-style
  text. Exports are part of the product, not an escape hatch added later.

## BK — Backups

- **BK-1** The application MUST create a timestamped backup before risky operations such as
  migrations and large imports.
- **BK-2** The application MUST provide a visible **Back Up Now** action.
- **BK-3** The application MUST clearly identify the database currently in use.
- **BK-4** Restoring a backup MUST be a documented, testable operation.
- **BK-5** A backup is not successful merely because a file was written. The format MUST be
  usable, the contents MUST be verifiable, and restoration MUST be practiced in development.
- **BK-6** A backup MUST NOT be openable for writing. It MUST be readable in place, and its
  records MUST be reachable by restoring it to a new file, which MUST NOT overwrite an existing
  one. The distinction MUST be carried by the file itself rather than by its name or location.

## CO — Correctness

- **CO-1** Money MUST be represented exactly. Binary floating-point arithmetic MUST NOT be used
  for monetary values.
- **CO-2** Transactions, splits, transfers, imports, balances, and reconciliations MUST have
  deterministic rules and focused tests.
- **CO-3** A write MUST NOT silently overwrite a change made since the writer last read the record.
  The household may have several browser tabs open on the same register, so concurrent edits are
  expected rather than exceptional. The application MUST detect the conflict and tell the user;
  discarding either edit without saying so is precisely the kind of quiet loss that costs the
  register its trust.

## PV — Privacy

- **PV-1** The application MUST NOT transmit transaction data, usage history, or diagnostics
  unless the user deliberately exports a file or explicitly chooses to send diagnostic
  information.
- **PV-2** The application MUST NOT contain analytics.
- **PV-3** The application MUST NOT contain advertisements, upgrade pressure, premium tiers, or
  expiring features, nor warnings designed to manufacture anxiety.
- **PV-4** An old release MUST NOT degrade, expire, or nag. A new release may improve the program;
  it may not punish someone for continuing to use the previous one.

## TS — Technology

- **TS-1** The implementation stack is plain Go, `net/http`, server-rendered HTML, small and
  purposeful HTMX interactions, SQLite, and ordinary files.
- **TS-2** The domain model MUST remain independent of the browser interface.
- **TS-3** A command-line tool or terminal interface MUST be able to use the same accounts,
  transactions, importers, exporters, and reconciliation logic without creating a second
  application.
- **TS-4** Every dependency and abstraction MUST earn its place. Prefer code that can still be
  understood after it has been left alone for a year.

## RP — Reports

- **RP-1** Reports MUST answer the questions a household actually asks: What is the current
  balance? Which transactions have not cleared? Where did the money go this month or year? How
  much did we spend in a category? What changed since the last statement?
- **RP-2** A report MUST earn its place by supporting a decision, explaining a balance, or helping
  find an error. Dashboards MUST NOT be created merely to fill space.

## SC — Scope

- **SC-1** Every proposed feature MUST answer three questions before it is built:
  1. Does it strengthen the register, reconciliation, import/export, or backup workflow?
  2. Can it remain understandable and dependable without an external service?
  3. Is its long-term maintenance cost justified for a small household application?

  A feature that fails these tests is outside the product, even if competing software includes it.

- **SC-2** The following are out of scope: investment tracking, market quotes, tax preparation,
  credit scores, bill payment, budgeting systems, debt coaching, bank synchronization, mobile
  social features, and artificial-intelligence advisers. Some may never be in scope.

- **SC-3** Version 1 is exactly: accounts, register, splits and categories, transfers,
  reconciliation, QIF/OFX/CSV import, search, export, and backups. Everything else must justify
  its existence against SC-1.

- **SC-4** Prefer omitting a feature to implementing it in a way that weakens trust in the
  register.
