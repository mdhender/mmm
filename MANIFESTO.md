# The Checkbook Manifesto

This document explains what we are building and why.
It is about direction and taste.

It is deliberately not the rulebook.
The checkable requirements — the ones a change either satisfies or violates — live in `SPECIFICATION.md`, and that is the document to argue with when something needs deciding.
This one is here to explain why those rules are what they are.

## We are building a checkbook, not a financial platform

Personal finance software used to do a small, important job well: it kept an accurate register, helped a household understand where its money went, and made reconciling a statement straightforward.

That is the application we want.

We are not trying to reproduce everything modern financial software has accumulated.
We are not building a bank, an investment terminal, a tax adviser, a shopping portal, or a subscription service.
We are building a dependable household checkbook.

It should feel calm.
It should behave predictably.
It should remain useful for decades.

## The household owns the records

The data belongs to the people whose money it describes.

A household must never be trapped by its checkbook software.
Open exports are part of the product, not an escape hatch added later, and the records must outlive our interest in maintaining the program.

If development stops tomorrow, the records must still be readable and recoverable.

## There is no subscription because there is no service to subscribe to

The application runs on the household's computers and stores the household's files.
It does not pretend that a local register is an ongoing service in order to justify an ongoing fee.

There will be no advertisements, upgrade pressure, premium tiers, expiring features, or warnings designed to manufacture anxiety.
A new release may improve the program, but an old release should not punish someone for continuing to use it.

The application works for the user.
The user does not work for the application.

## The register is the center

The primary screen is a check register.
It should be fast to scan, pleasant to edit, and difficult to misunderstand.

The first-class operations are the familiar ones — entering a transaction, splitting it, transferring between accounts, marking things cleared, reconciling, searching, importing, exporting, backing up.

These are not the humble beginnings of a larger financial empire.
They are the product.

## Reconciliation is a promise

A checkbook earns trust by answering a simple question: does our register agree with the institution's statement?

Reconciliation is therefore a central workflow, not a checkbox hidden among reports.
The application should make discrepancies visible without attempting to explain them away.

Finishing a reconciliation records a useful fact.
It does not rewrite history, insert mysterious adjustments, or silently change prior transactions.

The application will help the user find the truth; it will not manufacture agreement.

## Explicit beats automatic

Automation is useful only when it remains understandable and reversible.

We import files rather than connecting to banks.
That is a smaller promise, and one we can keep.
An import should show its work before it changes anything.

The program may remember useful choices and suggest likely categories, but the final record is the user's decision.

## Boring technology is a feature

The implementation should be easy to build, inspect, and keep running.
Plain Go, ordinary files, ordinary backups, and a stack small enough to hold in your head.

The application runs as a local program and opens its interface in the user's normal browser.
There is no server to install and no service to keep alive.

The domain model stays independent of the browser interface, so a command-line tool or terminal
interface can use the same logic without becoming a second application.

Every dependency and abstraction must earn its place.
We prefer code that can still be understood after it has been left alone for a year.

## Cross-platform means unsurprising

Windows and macOS are equal targets.

Moving the household records to a new computer should require copying files, not deactivating one machine, authorizing another, or contacting support.

Platform integration is welcome when it makes the application friendlier.
It is not welcome when it makes building and distributing the program fragile.

## Privacy follows from architecture

Financial records are sensitive.
The simplest privacy policy is to avoid collecting them.

We do not want analytics whose convenience depends on quietly observing the household.

Local-first is not a marketing claim.
It is the architecture.

## Backups must be obvious

An application that safeguards years of household records must make backups difficult to neglect and easy to understand.

A backup is not successful merely because a file was written.
Restoring one should be a documented, practiced operation rather than an emergency ritual.

## Correctness outranks cleverness

Money is represented exactly, never with binary floating-point arithmetic.
That is not a performance decision to be revisited; it is what makes the register worth trusting.

Destructive actions require confirmation.
Imports are repeatable.
Errors say what happened and what the user can safely do next.

We would rather omit a feature than implement it in a way that weakens trust in the register.

## Reports answer household questions

Reports should help the household answer questions it actually asks — what the balance is, what has not cleared, where the money went, what changed since the last statement.

We will not create dashboards merely to fill space.
A report earns its place by supporting a decision, explaining a balance, or helping find an error.

## Scope is defended, not merely declared

Every proposed feature has to justify itself, and the test is written down in `SPECIFICATION.md` so that it can be applied rather than merely felt.

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
