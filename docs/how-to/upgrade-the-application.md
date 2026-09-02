# How to upgrade the application

This guide shows you how to move to a newer version of the program without putting your records at
risk, and how to get back if the new version does not suit you.

## Before you start

Two facts shape everything below.

**The program does not back up your database before upgrading it.** It is supposed to, and one day
it will; today that step is yours. Do not skip it.

**A database is only ever upgraded, never downgraded.** Once a newer version has opened your file,
an older version will refuse to open it at all — it checks the schema version and stops rather
than guess, showing you a page that says so. That refusal protects the file, but it does not give
you a working checkbook: going back to an older version means going back to your backup, which is
why you take one first.

## 1. Stop the program

Press **Ctrl+C** in the terminal running it. Close any browser tabs showing the register, so you
are not tempted to trust a stale page later.

Confirm it is really stopped: in the database folder, `checkbook.db-wal`, `checkbook.db-shm`, and
`checkbook.db.lock` should all be gone, leaving `checkbook.db` on its own. A copy taken while
those files exist can be missing your most recent changes.

If starting the program later tells you the checkbook is already open, a copy is still running —
stop that one before going on.

## 2. Write down where you are

You need two things to tell afterwards whether the upgrade went well.

The version you are leaving:

```sh
go run ./cmd/checkbook -version
```

And a number you can check: start the program, note the **ending balance** of your busiest
account, then stop it again. Any figure you can compare will do; a balance is the one that would
matter most if it changed.

## 3. Copy the database

From the folder holding your database:

```sh
cp checkbook.db "checkbook-before-upgrade-$(date +%Y%m%d-%H%M%S).db"
```

On Windows PowerShell:

```powershell
Copy-Item checkbook.db "checkbook-before-upgrade-$(Get-Date -Format yyyyMMdd-HHmmss).db"
```

Put the copy somewhere the upgrade cannot reach — another folder, an external disk, a sync folder.
Not beside the original, where a mistaken `rm` takes both.

## 4. Get the newer version

There are no packaged releases yet, so this means updating your source checkout. From the
repository root:

```sh
git pull
```

If you have local changes you care about, commit or stash them first — `git pull` will refuse
rather than discard them, but you will be stuck mid-upgrade while you sort it out.

Nothing is compiled until you run it; `go run` rebuilds from whatever the checkout now contains.

## 5. Start it and let it upgrade the file

```sh
go run ./cmd/checkbook -db ~/Documents/checkbook/checkbook.db
```

The first start after an upgrade brings the database's schema up to date. It happens while the
program opens the file, before it serves anything, so if it fails you will see an error in the
terminal and no register — not a half-working page.

## 6. Check that nothing moved

Three checks, in order:

1. The version at the bottom of the register page is the new one.
2. The `database:` line names the file you meant, not a new empty one created by a changed default.
3. The ending balance you noted in step 2 is unchanged.

If all three hold, the upgrade is done.

## If something is wrong

Do not try to fix the database in place. Running the older version against the upgraded file will
not damage it — the program refuses a database newer than itself — but it will not open it either,
so it is not a way back.

1. Stop the program.
2. Move the current `checkbook.db` aside — rename it, do not delete it. It is evidence if you
   want to report the problem.
3. Put your backup copy back, named `checkbook.db`.
4. Return the checkout to the version you were on: `git checkout <previous-tag-or-commit>`.
5. Start the program and confirm the balance from step 2.

Then report what happened, including the version you moved from and to, and the error text if
there was one.

## Which backups to keep

Keep the pre-upgrade copy until you have used the new version for long enough to trust it — a few
sessions, or a full statement cycle if you reconcile monthly. Then keep it anyway if you have the
room; it costs a few hundred kilobytes and it is the only copy of that exact moment.

## Related

- [How to create your first checkbook](create-your-first-checkbook.md) — where the database lives
  and why it lives there
- [User manual](../references/user-manual.md) — options, messages, and what the program does with
  the file
