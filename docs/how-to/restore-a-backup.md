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

The register comes up in slate, with a padlock at the top reading **Backup — nothing can be
changed**. There is no entry form and no way to mark anything: nothing you do here can alter the
file.

Look at the accounts and the ending balances. Is this the day you want to be back at?

**The box is not a formality, and it is not optional either.** Opening a database normally brings
its schema up to date, which rewrites it — and a backup that has been rewritten is no longer the
backup you took. So the program will not do it: a backup opened without the box ticked is refused,
and the page tells you the two things you can do instead. Reading one is the only thing that can
be done to it in place.

If it is the wrong one, press **Close checkbook** again and try the next one.

## 2. Restore it to a new file

Press **Close checkbook** to let go of the backup. On the page you land on, under **Restore a
backup**, there are two boxes:

- **Restore from** — already filled in with the backup you were just reading.
- **Restore to** — the file to write. Type a name that does not exist yet, such as
  `~/Documents/checkbook/checkbook-restored.db`.

Press **Restore**.

The program copies the backup to that name, brings the copy up to date if it came from an older
release, and reads it back before giving it the name you asked for. **The backup itself is not
altered** — it is still a backup afterwards, and you can restore it again.

**Nothing is ever written over.** Restoring to a name that already exists is refused, which is
deliberate: you are doing this because something went wrong, and the file you would be replacing is
often the one that shows what. That is why you restore to a new name and put it in place yourself,
in step 4.

## 3. Open it and check it

The page names the file that was written and fills the box under **Open a checkbook** in with it.
Leave **Open read-only** unticked and press **Open**.

The register comes up on the restored records. Check the ending balance against what you saw in
step 1 — it should be the same number.

## 4. Put it where you keep your records

You now have three files: your old `checkbook.db`, the backup, and the restored copy. Decide which
is the one you will use from now on.

Press **Close checkbook** first, so nothing is holding either file open. Then, in your file manager
or a terminal:

```sh
cd ~/Documents/checkbook
mv checkbook.db checkbook-before-restore.db
mv checkbook-restored.db checkbook.db
```

On Windows PowerShell:

```powershell
cd ~\Documents\checkbook
Rename-Item checkbook.db checkbook-before-restore.db
Rename-Item checkbook-restored.db checkbook.db
```

Keep `checkbook-before-restore.db`. Even a damaged file is evidence, and a file you replaced by
mistake is only lost if you deleted it.

**Do this only while the checkbook is closed.** The page saying no checkbook is open is the
program telling you it is not holding the file: with it closed there is no `checkbook.db-wal`
beside the database waiting to be folded back in, so the single file is the whole checkbook.

Open it again, then take a backup of it, so the trail continues from here.

## If the backup will not open

The page says which of these it is, and what to do about it. In every case the file is left
exactly as it was found.

| What it says | What it means | What to do |
| --- | --- | --- |
| That file is a backup | You pressed **Open** without ticking **Open read-only**. | Tick the box to read it, or skip to step 2 and restore it. Nothing was written: a backup is never opened for writing, because the moment it were it would stop being the copy you took. |
| That backup was written by an older version of the program | The backup predates a change to the database's structure, and reading it in place cannot bring it up to date without rewriting it. | Skip step 1 and restore it. Restoring migrates the copy, which is the one place that is safe, and leaves the backup as it is. |
| This checkbook was written by a newer version of the program | The backup came from a later release than the one you are running. | Use that release with this file, or restore an older backup. |
| That file is not a checkbook | It is a SQLite file some other program made. | Check the path. Your backups are named `checkbook-YYYYMMDD-HHMMSS.db`. |
| There is no file there | The path is wrong. | Check it against your file manager. Nothing is ever created from a typo. |
| There is already a file at that path | The name under **Restore to** is taken. | Choose one that is not. The page suggests a free name beside the one you typed. |

## What a restore does not do

Restoring replaces everything. Anything entered after the backup was taken is not in it, and there
is no way to merge the two — the register does not guess at which of two versions of a record is
right (SPECIFICATION.md RC-1, SC-4).

That is the argument for backing up often. It costs one press.

## Next

- [How to create your first checkbook](create-your-first-checkbook.md) — where backups come from
- [User manual](../references/user-manual.md) — backing up, closing, and opening in full
