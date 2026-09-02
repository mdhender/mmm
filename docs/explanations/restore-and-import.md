# Restore and import

Two words in this program mean "get my records back", and they are not the same thing. This
explains which one you want and why the program will not let you have a third.

## The short answer

**Restore acts on files. Import acts on records.**

|  | Restore | Import |
| --- | --- | --- |
| What it moves | A whole checkbook file | A set of transactions |
| Where it reads from | A backup, or a checkbook, that this program wrote | QIF, OFX/QFX, CSV — a file some other program wrote |
| What it does to what you have | **Replaces** it: you get the records in the file | **Adds** to it: records join what is already there |
| Needs a checkbook open | No — it produces one | Yes — it puts records into one |
| Review before committing | The *file* is reviewed, by opening it and reading it | Mandatory: found, duplicates, errors |
| Doing it twice | Gives the same file both times | Must not create duplicates |
| Governed by | BK-4, BK-5, BK-6, BK-7 | IE-1 through IE-9 |

If you want the register as it was on a particular day, you want **restore**. If you want this
month's transactions from your bank added to what you already have, you want **import**.

Import does not exist yet. Restore does.

## Why keep them apart

Because they fail differently, and a household needs to know which failure they are looking at.

A restore that goes wrong leaves you with two whole files and a question about which one belongs
at your checkbook's name. That is recoverable by anyone who can rename a file, which is why the
program keeps both and tells you their names.

An import that goes wrong leaves you with one file containing some records twice, or some records
missing, or some records categorized as something they are not — mixed in among thousands of
others, with no obvious line between the ones that were there and the ones that arrived. That is
why IE-5 makes review mandatory before an import commits, and why IE-6 makes a repeated import a
no-op rather than a doubling.

Calling both of them "restore" would mean one word for two operations whose failure modes have
nothing in common. Somebody would reach for the one they knew the name of.

## The case that looks like both

**An old backup coming forward.** You restore a backup written by last year's release. The copy has
to be brought up to the schema this release understands before it can be opened. Is that an import
of a past version of ourselves?

No. It is *migration*, and it is an implementation detail of restore. Restore is deliberately the
only place in this program where an old schema is brought forward, because it is the only place
where doing so is not destroying the thing that was kept: the copy is migrated, and the backup is
left exactly as it was found. Opening a backup in place refuses an old schema for the same reason.

**Restore is where backward compatibility lives.** A backup from any release this program can still
migrate from is restorable, however old. That promise has one home, and it is `backup.Restore`.

## The case that is an import

**Lifting some records out of a backup into the checkbook you are working in.** One account. One
month. The three transactions you deleted by mistake in March.

That is not a restore, and it is not built. If it is ever built it is an **import**: the source
happens to be this program's own format, but the unit is records and the effect is additive, so
IE-5, IE-6 and IE-7 apply in full — a review screen showing what was found and what looks like a
duplicate, a second run that adds nothing, and no silent merging or categorizing.

This is written down as IE-9 so that it is not re-argued as a feature request against restore.

## Why there is no merge

The obvious-looking feature is a restore that keeps what you have *and* brings back what the backup
had. The program will not have one, for the same reason reconciliation will never write an
adjustment entry (RC-1).

A merge has to decide, for every record that differs between two files, which version is right. The
program cannot know. It would have to guess, and a register whose numbers came partly from a guess
is not a register you can hand to anyone. Where two files disagree, only a person knows which is
correct — and the way a person expresses that is by choosing a file (restore) or by choosing
records one at a time with duplicates flagged (import).

SC-4 is the rule this follows: prefer omitting a feature to weakening trust in the register.

## Where each one lives

- **Restore a backup** — in the sidebar under **This checkbook**, and on the page shown when no
  checkbook is open, including when the one you started on would not open. It lists the copies it
  can find; one press replaces your checkbook and keeps what was there.
- **Or restore to a file of its own** — below that list. It copies a backup to a name you choose
  and replaces nothing.
- **Import** — not built. It will live beside them when it is.

## Read next

- [How to restore a backup](../how-to/restore-a-backup.md) — the steps
- [Glossary](../references/glossary.md) — the words the interface uses
- `SPECIFICATION.md`, sections BK and IE — the rules this explains
