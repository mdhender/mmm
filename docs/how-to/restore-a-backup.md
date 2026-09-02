# How to restore a backup

This guide shows you how to go back to a backup: how to look at one first to be sure it is the
right one, how to put it in place, and how to keep the file you are replacing in case you were
wrong.

It takes a few minutes and needs no terminal. Do it once now, while nothing is at stake — an
untested backup is a guess.

## Before you start

You need a backup to restore. **Back up now**, in the sidebar under **This checkbook**, writes
one beside your database and names it for the moment it was taken:

```
~/Documents/checkbook/
    checkbook.db
    checkbook-20260902-141530.db      <- a backup
    checkbook-20260830-091204.db      <- an older one
```

If the folder holds no such file, take a backup first and come back once you have one to practise
on.

## 1. Look at the backup before you use it

You do not have to guess which backup is the right one, and you do not have to risk finding out
the hard way. Open it **read-only** and read it.

1. In the sidebar, press **Close checkbook** and confirm.
2. On the page you land on, type the backup's full path into the box —
   `~/Documents/checkbook/checkbook-20260902-141530.db` — and tick **Open read-only**.
3. Press **Open**.

The register comes up in slate, with a padlock at the top reading **Read-only — nothing can be
changed**. There is no entry form and no way to mark anything: nothing you do here can alter the
file.

Look at the accounts and the ending balances. Is this the day you want to be back at?

**Read-only is not a formality.** Opening a database normally brings its schema up to date, which
rewrites it — and a backup that has been rewritten is no longer the backup you took. Reading one is
the only thing you should ever do to it in place.

If it is the wrong one, press **Close checkbook** again and try the next one.

## 2. Keep the file you are about to replace

Before anything is overwritten, put the current `checkbook.db` somewhere safe. Even a damaged file
is evidence, and a file you replaced by mistake is only lost if you deleted it.

Press **Close checkbook**, so nothing is holding the file open, then rename it in your file
manager:

```
checkbook.db  ->  checkbook-before-restore.db
```

On macOS or Linux:

```sh
cd ~/Documents/checkbook
mv checkbook.db checkbook-before-restore.db
```

On Windows PowerShell:

```powershell
cd ~\Documents\checkbook
Rename-Item checkbook.db checkbook-before-restore.db
```

Do not skip this. It costs one command and it is the difference between a restore you can undo and
one you cannot.

## 3. Put the backup in place

Copy — do not move — the backup to the name your program opens:

```sh
cp checkbook-20260902-141530.db checkbook.db
```

On Windows PowerShell:

```powershell
Copy-Item checkbook-20260902-141530.db checkbook.db
```

Copy rather than move so the backup is still a backup afterwards. If the restore goes wrong you
will want it a second time.

**Do this only while the checkbook is closed.** The page saying no checkbook is open is the
program telling you it is not holding the file: with it closed there is no `checkbook.db-wal`
beside the database waiting to be folded back in, so the single file is the whole checkbook.

## 4. Open it

Back in the browser, type `~/Documents/checkbook/checkbook.db` into the box, leave **Open
read-only** unticked, and press **Open**.

The register comes up on the restored records. Check the ending balance against what you saw in
step 1 — it should be the same number.

Then take a backup of the restored file, so the trail continues from here.

## If the backup will not open

The page says which of these it is, and what to do about it. In every case the file is left
exactly as it was found.

| What it says | What it means | What to do |
| --- | --- | --- |
| That backup was written by an older version of the program | The backup predates a change to the database's structure, and read-only cannot bring it up to date without rewriting it. | Copy it to a new name and open the copy with **Open read-only** unticked. Opening it normally migrates it, and doing that to a copy leaves the backup itself untouched. |
| This checkbook was written by a newer version of the program | The backup came from a later release than the one you are running. | Use that release with this file, or restore an older backup. |
| That file is not a checkbook | It is a SQLite file some other program made. | Check the path. Your backups are named `checkbook-YYYYMMDD-HHMMSS.db`. |
| There is no file there | The path is wrong. | Check it against your file manager. Read-only never creates a file, so a typo is reported rather than turned into an empty checkbook. |

## What a restore does not do

Restoring replaces everything. Anything entered after the backup was taken is not in it, and there
is no way to merge the two — the register does not guess at which of two versions of a record is
right (SPECIFICATION.md RC-1, SC-4).

That is the argument for backing up often. It costs one press.

## Next

- [How to create your first checkbook](create-your-first-checkbook.md) — where backups come from
- [User manual](../references/user-manual.md) — backing up, closing, and opening in full
