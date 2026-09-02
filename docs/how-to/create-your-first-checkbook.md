# How to create your first checkbook

This guide shows you how to set up the database file that will hold your household's records, put
it somewhere you can protect, and confirm the program is using it.

## Before you start

This guide takes you from nothing to an account you can enter transactions into. The file you make
here is the one your records will live in.

You need a source checkout of the project and a working Go toolchain. Run every command below
from the repository root.

## 1. Choose where the file will live

Put the file in a folder that belongs to your documents, not to the program. Somewhere you would
notice, and somewhere your usual backups already cover:

- macOS: `~/Documents/checkbook/`
- Windows: `%USERPROFILE%\Documents\checkbook\`

Two places to avoid:

- **Inside the source checkout.** The next time you clean or re-clone it, your records are in the
  blast radius.
- **Inside a folder that syncs continuously** — Dropbox, iCloud Drive, OneDrive, Google Drive.
  While the program runs, the database is spread across the main file and a `-wal` companion, and
  a sync service that copies them at different moments can capture a file that no longer makes
  sense. Sync folders are a fine home for the backup *copies* the program makes for you, which is
  covered in step 6.

## 2. Create the folder yourself

The program creates the database file but never creates directories — a mistyped path is reported,
not built. So make the folder first:

```sh
mkdir -p ~/Documents/checkbook
```

On Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force ~\Documents\checkbook
```

## 3. Start the program pointed at the new file

```sh
go run ./cmd/checkbook -db ~/Documents/checkbook/checkbook.db
```

The file is created, and your browser opens at `http://127.0.0.1:8842/`. Because the database is
empty, the page says there are no accounts yet and offers to add one — that is the expected result
at this stage.

**Bookmark that address.** It is the same every time the program runs, so it is how you get back
to the register after closing the browser without starting a second copy.

If you would rather it did not open a browser, add `-open=false` and use the address it prints.

## 4. Create your first account

Follow **Add your first account**, or go to `http://127.0.0.1:8842/accounts/new`.

An account is one statement: one card, or one account at one bank. Fill in four fields:

- **Name** — what you call it. The name on the statement, or simply `Checking`.
- **Kind** — checking, savings, credit card, or cash.
- **Currency** — the currency the statement is in. Every amount in this account is held in it.
- **Opening balance** — what the account held before the first transaction you are going to enter.
  Leave it empty to start at zero. Put a minus sign in front of it for a card you already owe on.

Press **Create**, and the browser goes to the account's register, with the opening balance as its
starting balance.

Choose these carefully. **This release cannot change an account once it is made** — not the name,
the kind, the currency, or the opening balance — and it cannot delete one either. If you get one
wrong now, while the database is otherwise empty, the quickest fix is to stop the program, delete
the file, and start again from step 3.

A household with several accounts adds them the same way: **+ Add account**, below the account
list, is on every page.

## 5. Confirm it is the file you meant

Two places name the database, and they should agree with the path you chose:

- the `database:` line the program printed in the terminal
- the bottom of the register page in the browser

Check this now. It is the one habit that keeps you from carefully backing up a file you are not
actually using.

## 6. Make your first backup

In the sidebar, under **This checkbook**, press **Back up now**.

The program writes a copy beside your database, named for the moment it was taken —
`checkbook-20260902-141530.db`. It does not stop to do it, and you do not have to close anything
first. The page comes back naming the file it wrote.

That copy is a complete backup. There is nothing else to export and no format to convert: the
whole checkbook is that one file. It is a **verified** copy — the program reopened it and read it
back before giving it a backup's name, so a copy that would not open is deleted rather than left
sitting in your folder looking like insurance.

Now look in the folder. Beside `checkbook.db` you will also see `checkbook.db-wal` and
`checkbook.db-shm` while the program is running; the backup has no such companions, which is what
makes it a single file you can copy anywhere.

**Copy that backup somewhere else** — another disk, or a sync folder — while you are thinking of
it. A backup on the same disk as the original protects you from mistakes, not from the disk.

Practise the restore once, now, while the stakes are zero.
[How to restore a backup](restore-a-backup.md) is four steps and takes a minute. An untested
backup is a guess.

## 7. See what a filled-in register looks like

A household with a year of records in it looks like this. Press **Close checkbook** in the
sidebar, confirm, and then choose **Open the sample household instead** on the page you land on.

The sample is held in memory: it reads and writes no files, and every page is marked amber so it
cannot be mistaken for your own register. Your checkbook is one box away — the page you closed
from remembers its path — so you can go back whenever you like.

If you would rather have both at once, start a second copy in another terminal:

```sh
go run ./cmd/checkbook -demo
```

That serves the sample at `http://127.0.0.1:8843/`, on its own port, so your own register can stay
open beside it.

## 8. Keep the command where you can find it

You will type the same command every time. Put it in a shell alias, a note, or a one-line script:

```sh
alias checkbook='cd ~/src/mmm && go run ./cmd/checkbook -db ~/Documents/checkbook/checkbook.db'
```

## Next

- [How to restore a backup](restore-a-backup.md) — practise it before you need it
- [How to upgrade the application](upgrade-the-application.md) — what to do before you move to a
  newer version
- [User manual](../references/user-manual.md) — every option, column, and message
