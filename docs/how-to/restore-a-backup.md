# How to restore a backup

This guide shows you how to go back to a backup: how to look at one first to be sure it is the
right one, how to put it in place in one press, and where the file you replaced goes.

It takes a few minutes and needs no terminal. Do it once now, while nothing is at stake — an
untested backup is a guess.

## Before you start

You need a backup to restore. **Back up now**, in the sidebar under **This checkbook**, writes one
into a `backups` folder beside your database and names it for the moment it was taken:

```
~/Documents/checkbook/
    checkbook.db                            your checkbook
    backups/
        checkbook-20260902-141530.db        a backup
        checkbook-20260830-091204.db        an older one
```

If the folder is not there, press **Back up now** once and it will be made for you; the page says
so when it does. Backups written by older releases sit beside `checkbook.db` rather than inside
`backups/`. They are not moved, and they are still found and still offered.

## 1. Open the list

In the sidebar, under **This checkbook**, press **Restore a backup**.

The page lists every copy this program can find, newest first, with when it was taken, how big it
is, and where it is. It reads both places — the folder beside your checkbook and the `backups`
folder inside it — and it decides what is a backup by looking inside each file, not by its name.
A backup you renamed is still listed; a text file you named `checkbook-20260101-000000.db` is not.

Your own `checkbook.db` is not in the list. It is the file being replaced, not a copy of itself.
Files named `checkbook-replaced-…` are, though: those are checkbooks that earlier restores set
aside, and restoring one is how you undo a restore.

**The list is also there when nothing is open.** If your checkbook will not open at all — the file
was truncated, or overwritten, or is not a database any more — the program still starts, still
says what is wrong, and puts this same list on the page it shows you. That is the day this feature
is for.

## 2. Look at a backup first, if you want to be sure

You do not have to guess which backup is the right one. You can read one before you use it.

1. Press **Close checkbook** and confirm.
2. On the page you land on, type the backup's full path into the box under **Open a checkbook** —
   `~/Documents/checkbook/backups/checkbook-20260902-141530.db` — and press **Open**.

The register comes up in slate, with a padlock at the top reading **Backup — nothing can be
changed**. There is no entry form and no way to mark anything: nothing you do here can alter the
file. Look at the accounts and the ending balances. Is this the day you want to be back at?

A backup says in its own header that it is one, so it is opened for reading whether you tick
**Open read-only** or not, and nothing is ever written to it. That is not a nicety: opening a
database normally brings its schema up to date, which rewrites it, and a backup that has been
rewritten is no longer the backup you took.

When you are done, press **Close checkbook**, open your own checkbook again, and go back to
step 1.

## 3. Restore it

Choose a backup in the list and press **Restore this backup**.

The program asks first. The confirmation names the file the records will come from, the file they
will replace, and says plainly that anything entered since that backup was taken is not in it and
that there is no way to merge the two. **Keep what I have** leaves everything as it is.

Press **Restore it**.

## 4. Read the answer

You land on your register, showing the restored records, at the same file name as before —
`checkbook.db` is still `checkbook.db`. A notice above it names two files:

- the backup the records came from, and
- **the checkbook you had**, kept beside your database as `checkbook-replaced-20260902-153104.db`.

Nothing was deleted. That kept file is an ordinary checkbook, not a backup, so if you decide you
were wrong you open it directly — type its path into the box under **Open a checkbook** — rather
than restoring it. It is also in the list on the restore page, if you would rather put it back in
one press.

Check the ending balance against what you saw in step 2. Then press **Back up now**, so the trail
continues from here.

## What happens underneath

Worth knowing, because it decides what state you are in if something goes wrong.

1. The backup is copied to a new file **while your checkbook is still open and working**. This is
   the slow part and the part most likely to fail, and it fails harmlessly: if the backup is
   damaged, or the disk is full, or the folder cannot be written to, you are told so with your
   checkbook still in front of you and nothing about it changed.
2. Only then is your checkbook closed, moved aside under its `checkbook-replaced-…` name, and the
   copy moved into its place. Both are renames in one folder, so they take microseconds.
3. Your checkbook is opened again and you land on it.

Its write-ahead log, if it had one, travels with it: you will see a `checkbook-replaced-….db-wal`
beside the file it belongs to. That is correct and it matters — a log left behind would be
adopted by the wrong database.

If a step fails, the program says which, and puts everything back. In every case both files are
still on disk and nothing is deleted.

## If it will not restore

The page says which of these it is, and what to do about it.

| What it says | What it means | What to do |
| --- | --- | --- |
| Your checkbook is still open and nothing about it has changed | The copy failed before anything moved. | Read the rest of the sentence — it says whether the backup is damaged, the disk full, or the folder unwritable. Try an older backup. |
| That backup was written by an older version of the program | The backup predates a change to the database's structure. | Restore it. Restoring brings the copy up to date, which is the one place that is safe, and leaves the backup as it is. Reading it in place is refused for the same reason. |
| This checkbook was written by a newer version of the program | The backup came from a later release than the one you are running. | Use that release with this file, or restore an older backup. |
| That file is not one of the backups listed below | The file moved or was deleted since the page was drawn. | Reload the page and choose from the list as it is now. |
| This page was drawn for a different checkbook | Another window closed or opened a checkbook after this page was drawn. | Reload the page and press again. Nothing was changed. |
| Your checkbook could not be moved aside | Another program has the file open. Most often this is a backup tool or a search indexer, and on Windows it can be transient. | Close whatever has it, and press again. Nothing was replaced. |
| The sample household is open | `-demo` keeps nothing on disk, so there is no file for a restored copy to replace. | Start the program on your own checkbook. |

## Restoring to a file of its own

Below the list there is a second form, **Or restore to a file of its own**. It takes a backup and a
name to write, copies one to the other, and replaces nothing:

- Use it when the file you want back is not the one you are working in — reading last year's
  records without disturbing this year's, say.
- Use it when the backup is on another disk, since the list only reads the checkbook's own folder.
- The name you give must not exist yet. Nothing is ever written over.

You then open the copy yourself.

## What a restore does not do

Restoring replaces everything. Anything entered after the backup was taken is not in it, and there
is no way to merge the two — the register does not guess at which of two versions of a record is
right (SPECIFICATION.md RC-1, BK-7, SC-4).

That is the argument for backing up often. It costs one press.

Lifting *some* records out of a backup — one account, one month — would be an import rather than a
restore, and it is not built. See
[Restore and import](../explanations/restore-and-import.md) for why the difference matters.

## Next

- [Restore and import](../explanations/restore-and-import.md) — why there are two words for
  getting your records back
- [How to create your first checkbook](create-your-first-checkbook.md) — where backups come from
- [User manual](../references/user-manual.md) — backing up, closing, and opening in full
