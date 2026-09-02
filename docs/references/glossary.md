# Glossary

The words this program's interface uses, and what each one means here. Some of them mean something
slightly different in other checkbook programs; where that is so, this says which meaning applies.

Words for things that do not exist yet are marked **not built**. They are listed anyway, so that
the vocabulary is settled before the screens are.

---

**Account** — one place money sits or is owed: a checking account, a savings account, a credit
card. Every account has one currency, and every transaction in it is in that currency. Accounts are
listed down the left of every page.

**Backup** — a copy of a checkbook, taken by **Back up now**, written into the `backups` folder
beside the checkbook and named for the moment it was taken:
`backups/checkbook-20260902-141530.db`. A backup is never opened for writing: it says in its own
header that it is a backup, so the program opens it for reading whether you ask it to or not. The
moment it were written to, it would stop being the copy you took.

**Category** — what a transaction was for: Groceries, Utilities, Salary. A transaction has one
category, or several if it is a **split**, or none, in which case the register says *Uncategorized*
rather than leaving the cell blank.

**Checkbook** — the file holding your records, and the whole of them: accounts, transactions,
splits, categories. One file, ordinarily called `checkbook.db`. The bottom of every page names the
one this window is editing.

**Cleared** — the bank has shown this transaction on your statement. A lowercase `c` in the
register's status column. Cleared is not the same as **reconciled**, and the register keeps them
apart.

**Close** — let go of the checkbook without stopping the program. Nothing is changed and nothing is
deleted; the file becomes one file you can copy, move, or replace, because there is no write-ahead
log beside it waiting to be folded back in. You can open it again, or open another, without
restarting.

**Demo** — the sample household, served by starting the program with `-demo`. It is held in memory,
nothing is written to disk, and it is discarded when the program stops. Every page carrying it is
marked, top and bottom, in amber.

**Ending balance** — every transaction in the account, added up. What you have, according to your
own records. Shown beside the **cleared balance**, which counts only the transactions the bank has
shown.

**Ephemeral** — held in memory and kept nowhere. Used of the demo. The mark is a property of the
database rather than of how the program was started, so anything that keeps nothing says so.

**Export** — writing your records out in a format other programs read: CSV, Ledger-style text.
**Not built.**

**Import** — adding transactions from a file some other program wrote — QIF, OFX/QFX, CSV — to a
checkbook you have open. Import *adds*; **restore** *replaces*. **Not built.** See
[Restore and import](../explanations/restore-and-import.md).

**Read-only** — opened so that nothing can be changed. A backup is always opened this way; an
ordinary checkbook can be, by ticking the box on the open form, which is the safe way to look at
one you do not mean to touch. A read-only register withholds every action that writes rather than
offering it and then refusing.

**Reconcile** — agreeing your register with a bank statement, line by line, and recording that you
did. **Not built.** When it is, it will never manufacture agreement: no adjustment entries, and no
edits to transactions you already reconciled.

**Reconciled** — recorded as agreeing with a statement. A capital `R` in the status column.
Stronger than **cleared**: cleared says the bank showed it, reconciled says you checked it against
a statement and the statement balanced.

**Register** — the list of transactions in one account, in date order, with a running balance. The
main screen.

**Restore** — replacing a checkbook with the records from a backup. Restore acts on a whole file:
it never merges, never alters the backup it reads, and always keeps the file it displaces, under a
`checkbook-replaced-…` name it tells you. There is a second form of it — *restore to a file of its
own* — which writes a copy under a name you choose and replaces nothing. See
[How to restore a backup](../how-to/restore-a-backup.md).

**Running balance** — the balance after each transaction, down the right of the register. It is
computed once, in one place, so every interface shows the same number.

**Split** — one transaction divided among several categories: a shop where half was groceries and
half was household. The register shows *— Split —* rather than naming one of them, because naming
the first would suggest the whole amount went there. The parts must add up to the transaction's
amount.

**Statement date** — the closing date of a bank statement, used in reconciling. A calendar date,
not an instant: it is never shifted into a timezone.

**Transaction** — one movement of money in one account, on one date: a payment, a deposit. Amounts
are exact to the cent — never a floating-point number.

**Transfer** — one movement of money between two of your own accounts, recorded once rather than as
two unrelated transactions. **Not built.**

**Uncleared** — the bank has not shown it yet. Blank in the status column. The count of uncleared
transactions is shown under the totals.

---

## Read next

- [User manual](user-manual.md) — what each screen does
- [Restore and import](../explanations/restore-and-import.md) — the one pair of words most easily
  confused
