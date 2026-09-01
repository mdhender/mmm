# The Checkbook Manifesto

## We are building a checkbook, not a financial platform

Personal finance software used to do a small, important job well: it kept an accurate register, helped a household understand where its money went, and made reconciling a statement straightforward.

That is the application we want.

We are not trying to reproduce everything modern financial software has accumulated.
We are not building a bank, an investment terminal, a tax adviser, a shopping portal, or a subscription service.
We are building a dependable household checkbook for Windows and macOS.

It should feel calm.
It should behave predictably.
It should remain useful for decades.

## The household owns the records

The data belongs to the people whose money it describes.

The application will keep its canonical records in a local SQLite database.
It will not require a vendor account, a cloud service, or an internet connection.
The database will be an ordinary file that can be copied, backed up, and restored without asking anyone's permission.

Open exports are part of the product, not an escape hatch added later.
The application should be able to export useful, documented formats such as CSV and Ledger-style text.
A household must never be trapped by its checkbook software.

If development stops tomorrow, the records must still be readable and recoverable.

## There is no subscription because there is no service to subscribe to

The application runs on the household's computers and stores the household's files.
It does not pretend that a local register is an ongoing service in order to justify an ongoing fee.

There will be no advertisements, upgrade pressure, premium tiers, expiring features, or warnings designed to manufacture anxiety.
A new release may improve the program, but an old release should not punish someone for continuing to use it.

The application works for the user.
The user does not work for the application.

## The register is the center

The primary screen is a check register: date, payee, category, memo, amount, status, and running balance.
It should be fast to scan, pleasant to edit, and difficult to misunderstand.

The first-class operations are the familiar ones:

- create and edit accounts;
- enter, change, and remove transactions;
- split a transaction among categories;
- record transfers between accounts;
- mark transactions cleared;
- reconcile an account against a statement;
- search and filter the register;
- import transactions from files;
- export records and create backups.

These are not the humble beginnings of a larger financial empire.
They are the product.

## Reconciliation is a promise

A checkbook earns trust by answering a simple question: does our register agree with the institution's statement?

Reconciliation will therefore be treated as a central workflow, not a checkbox hidden among reports.
The application should always show the statement balance, the cleared balance, and the remaining difference.
It should make discrepancies visible without attempting to explain them away.

Finishing a reconciliation records a useful fact.
It does not rewrite history, insert mysterious adjustments, or silently change prior transactions.

The application will help the user find the truth; it will not manufacture agreement.

## Explicit beats automatic

Automation is useful only when it remains understandable and reversible.

The initial application will import QIF, OFX/QFX, and CSV files rather than connect directly to financial institutions.
Before committing an import, it should show what it found, identify possible duplicates, and let the user review the result.

It will not store bank credentials.
It will not depend on a third-party aggregation service.
It will not silently download, categorize, merge, or delete transactions.

The program may remember useful choices and suggest likely categories, but the final record is the user's decision.

## Boring technology is a feature

The implementation should be easy to build, inspect, and keep running:

- plain Go;
- `net/http`;
- server-rendered HTML;
- small, purposeful HTMX interactions;
- SQLite;
- ordinary files and ordinary backups.

The application will run as a local program, bind only to the loopback interface, and open its interface in the user's normal browser.
It should not require a database server, Node.js, a browser extension, an installer framework, or a permanent background service.

The domain model will remain independent of the browser interface.
A command-line tool or terminal interface may use the same accounts, transactions, importers, exporters, and reconciliation logic without creating a second application.

Every dependency and abstraction must earn its place.
We prefer code that can still be understood after it has been left alone for a year.

## Cross-platform means unsurprising

Windows and macOS are equal targets.
A release should be an ordinary native executable accompanied by clear instructions about where the data lives and how to back it up.

The same database format and behavior should work on both systems.
Moving the household records to a new computer should require copying files, not deactivating one machine, authorizing another, or contacting support.

Platform integration is welcome when it makes the application friendlier.
It is not welcome when it makes building and distributing the program fragile.

## Privacy follows from architecture

Financial records are sensitive.
The simplest privacy policy is to avoid collecting them.

The application will not transmit transaction data, usage history, or diagnostics unless the user deliberately exports a file or explicitly chooses to send diagnostic information.
It will not contain analytics whose convenience depends on quietly observing the household.

Local-first is not a marketing claim.
It is the architecture.

## Backups must be obvious

An application that safeguards years of household records must make backups difficult to neglect and easy to understand.

The program should create timestamped backups before risky operations such as migrations and large imports.
It should provide a visible **Back Up Now** action and clearly identify the database currently in use.
Restoring a backup should be a documented, testable operation rather than an emergency ritual.

A backup is not successful merely because a file was written.
The format must be usable, the contents must be verifiable, and restoration must be practiced in development.

## Correctness outranks cleverness

Money will be represented exactly, never with binary floating-point arithmetic.
Transactions, splits, transfers, imports, balances, and reconciliations will have deterministic rules and focused tests.

Database migrations will be explicit.
Destructive actions will require confirmation.
Imports will be repeatable without quietly creating duplicates.
Errors will say what happened and what the user can safely do next.

We would rather omit a feature than implement it in a way that weakens trust in the register.

## Reports answer household questions

Reports should help the household answer questions it actually asks:

- What is the current balance?
- Which transactions have not cleared?
- Where did the money go this month or year?
- How much did we spend in a category?
- What changed since the last statement?

We will not create dashboards merely to fill space.
A report earns its place by supporting a decision, explaining a balance, or helping find an error.

## Scope is defended, not merely declared

Every proposed feature must answer three questions:

1. Does it strengthen the register, reconciliation, import/export, or backup workflow?
2. Can it remain understandable and dependable without an external service?
3. Is its long-term maintenance cost justified for a small household application?

A feature that fails these tests is outside the product, even if competing software includes it.

Investment tracking, market quotes, tax preparation, credit scores, bill payment, budgeting systems, debt coaching, bank synchronization, mobile social features, and artificial-intelligence advisers are not part of the initial application.
Some may never be.

Saying no is how the program remains small enough to trust.

## Our measure of success

This project succeeds when two people can use it for their real household accounts without thinking about the software very often.

They can enter a transaction quickly.
They can import a statement without fear.
They can reconcile to zero and understand why.
They know where their data is.
They know how to back it up.
They can move it to another computer.
They are never pressured to subscribe, upgrade, connect an account, or surrender their records.

The application does not need to transform personal finance.

It needs to keep the checkbook faithfully—and keep doing so.
