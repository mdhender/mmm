# About mmm

`mmm` is a household checkbook. It keeps a register of what you spent and received, lets you
reconcile that register against a statement, and stores the whole thing in one ordinary file on
your own computer.

That description is short on purpose. What follows is why it is short, what that costs, and who
would be better served by something else.

## A checkbook, not a financial platform

Personal finance software used to do a small, important job well. It kept an accurate register,
it helped you see where the money went, and it made reconciling a statement a five-minute job
rather than an afternoon. Much of it has since grown into something else — a place to check credit
scores, receive offers, track investments, be advised. The register is still in there somewhere,
usually a few screens deep.

`mmm` is an attempt to keep the original job and decline the rest. The register is not the humble
first version of a financial empire; it is the product. Splits, transfers, marking things cleared,
reconciling, importing a statement file, exporting, backing up — that list is the whole ambition.

This is a real trade-off, not a claim of superiority. Software that does more is more useful to
people who want more. The bet here is that a household that mainly wants to know what its balance
is, and whether the bank agrees, is poorly served by carrying the rest of it around.

## Your records are files, and the files are yours

Everything lives in a single SQLite database. Not a proprietary container, not a folder of
fragments, not a row in someone's cloud table — one file, in a format that has been stable for
twenty years and that a dozen ordinary tools can open.

The practical consequences are worth stating plainly:

- Backing up is copying a file. Restoring is copying it back. There is nothing to export first.
- Moving to a new computer is copying a file. No deactivating one machine, authorizing another, or
  contacting anyone.
- If this project stops tomorrow — and small projects do stop — your records are still there and
  still readable. You would lose a nice way to look at them, not the records themselves.

That last point is the one that shapes the most decisions. A checkbook holds years of a
household's history, and any design that makes those years hostage to the program's continued
existence has failed at the only job that really matters.

## Why it does not connect to your bank

Most finance software now offers to log into your accounts and pull transactions down for you.
That is genuinely convenient, and it is fair to say that giving it up is the biggest thing you
give up here.

`mmm` is built to import files instead: the QIF, OFX/QFX, or CSV your institution lets you
download. You do the downloading. (That importer is not written yet — see below.)

The reasoning is that a direct connection is a promise this program cannot keep. It would mean
holding your bank credentials, or depending on an aggregation service that holds them for you.
Those services change their terms, get acquired, lose access to institutions, and occasionally
lose data. A local program that reads a file you already have is a smaller promise, and a smaller
promise is one that still works in ten years.

There is a privacy dimension too, though it follows from the architecture rather than motivating
it. The simplest way to avoid mishandling financial records is not to collect them. There is no
account, no telemetry, no analytics, and nothing to transmit — not as a policy that could be
revised, but because there is no server on the other end to revise it.

## Reconciliation is the point, not a feature

A checkbook earns its keep by answering one question: does my register agree with the statement?

So reconciliation is a central workflow rather than something filed under reports, and it comes
with a rule that sounds restrictive until you have been burned by the alternative. When your
register and the statement disagree, the program will show you the gap. It will not close it for
you. No adjustment entry appears, no prior transaction is quietly corrected, no difference is
rounded into agreement.

This is deliberate, and it is occasionally annoying. A discrepancy usually means a real mistake —
a transaction entered twice, a missing deposit, a transposed figure — and finding it is the work
that makes the register trustworthy. Software that manufactures agreement saves you ten minutes
once and costs you the ability to believe any balance it shows you afterwards.

You can see this principle in the register today: the ending balance and the cleared balance are
shown side by side, with the difference between them named. One number would be tidier. Two
numbers and their gap is the truth.

## Money is exact

Amounts are stored as whole counts of cents, not as decimal fractions of dollars. `0.1 + 0.2` in
binary floating point is famously not `0.3`, and a register that drifts by a hundredth of a cent
per operation is a register you will eventually stop trusting for reasons you cannot name.

This is not an optimization to revisit later. It is close to the definition of the program working
at all.

## Why it runs in your browser without being on the internet

Starting `mmm` launches a small web server on your own machine and opens your normal browser at
it. Nothing is installed, no service runs in the background, and the address is bound to the
loopback interface — reachable from your computer and from nowhere else.

The browser is here as a rendering engine you already have and already know, not as a gateway to
anything. It saves shipping a user interface toolkit, and it means the register looks and behaves
like every other page you use.

One honest consequence: there is no password, no login, and no session. Adding them would be
security theatre — the server has no remote origin and no notion of separate users, so there is no
second person for a login to distinguish. What protects your records is that the program is only
reachable from your machine. What that means in practice is that anyone who can already use your
computer can read your checkbook while it is running. If that matters in your household, the
answer is your operating system's account separation and disk encryption, not a password box on a
page only you can reach.

## What this is not, and who should look elsewhere

Firmly out of scope, some of it permanently: investment tracking, market quotes, tax preparation,
credit scores, bill payment, envelope budgeting, debt coaching, bank synchronization, and advice
of any kind, artificial or otherwise.

If you want to watch a portfolio, plan a budget in categories with rollovers, or have transactions
appear without your doing anything, `mmm` will frustrate you, and one of the larger products will
serve you better. That is not a failure on either side. Saying no to features is how this one stays
small enough to be understood, and something small enough to be understood is the only kind of
program that can still be trusted after being left alone for a year.

## Where it actually is today

Worth being blunt, because everything above describes the intent and the software is not there
yet.

Today `mmm` **displays** a register: accounts, transactions with running balances, cleared and
uncleared totals, from a database file. It cannot yet create an account, enter or edit a
transaction, import, export, reconcile, search, or make a backup for you. The version number
starts with a zero and ends in `beta` for good reason.

What does exist is the part everything else has to stand on: exact money, a schema that refuses to
store an amount as anything but an integer, identifiers that are never reused, and a register whose
arithmetic lives in one place so that a terminal interface will show the same numbers as the
browser. The interesting work — entering, importing, reconciling — is ahead.

## The measure

This project succeeds if two people can use it for their real household accounts without thinking
about the software very often. They enter a transaction quickly. They import a statement without
fear. They reconcile to zero and understand why. They know where their data is, how to back it up,
and how to move it to another computer. They are never pressured to subscribe, upgrade, connect an
account, or hand over their records.

It does not need to transform personal finance. It needs to keep the checkbook faithfully, and
keep doing it.

## Related reading

- [User manual](../references/user-manual.md) — what the program does, precisely
- [How to create your first checkbook](../how-to/create-your-first-checkbook.md)
- [How to upgrade the application](../how-to/upgrade-the-application.md)
- [`MANIFESTO.md`](../../MANIFESTO.md) — the same convictions, written for the people building it
- [`SPECIFICATION.md`](../../SPECIFICATION.md) — the same convictions, written as numbered rules
