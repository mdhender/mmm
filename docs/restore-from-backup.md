# Restoring from a backup

Design intent for the slice that turns restore from two typed paths and two `mv`s into one press.
Like the other notes directly under `docs/`, this is **not binding** — where it conflicts with
`SPECIFICATION.md`, the specification wins.

**Built in 0.21.0-beta.** This note is kept as the record of the argument, not as a description of
the program: for that, read
[the user manual](references/user-manual.md#restoring-a-backup),
[how to restore a backup](how-to/restore-a-backup.md), and
[restore and import](explanations/restore-and-import.md). Three things were decided differently
while building it, and are noted here rather than silently:

- **There is no "Somewhere else?" box** on the list. A typed path cannot be checked against a
  listing the program just made, which is the whole guard on the one-press restore, and the
  restore-to-a-copy form below already covers a backup kept on another disk.
- **`backup.Replace` picks the kept name itself**, deriving the stamp from the restored file's
  name (`RestoredName` is exported so the caller picks that one first). The pair still share a
  stamp; the signature stays the two arguments section 6 gives it.
- **`storage.Open` had to be fixed first.** It did not merely serve the wrong page for a corrupt
  checkbook — it hung. `sqlitemigration.Pool.Take` retries a pool it could not open every five
  seconds for as long as its context lasts, so a truncated or overwritten checkbook produced a
  listener that accepted and never answered. The header is now checked before sqlitemigration sees
  the file. Without that, prerequisite one delivers a page nobody can reach.

---

## Why

Restoring works today and ends in a terminal.

`backup.Restore(ctx, src, dest)` copies a backup to a new file, stamps it a checkbook, migrates it
forward, verifies it by reopening, and refuses to write over anything
(`internal/backup/restore.go`). `POST /checkbook/restore` offers that as two text boxes on the page
shown when no checkbook is open. That satisfies BK-4, and it is where the work stops.
`docs/how-to/restore-a-backup.md` has to finish the job in prose:

```sh
mv checkbook.db checkbook-before-restore.db
mv checkbook-restored.db checkbook.db
```

PL-4 exists because the household double-clicks an executable, and the manifesto asks that restoring
be "a documented, practiced operation rather than an emergency ritual." Two `mv`s, in the right
order, at the worst moment, is an emergency ritual. Three costs come with it:

- **Both boxes are typed.** The reader has to find the backup in a file manager and copy its name
  across, at a moment when they may not know which backup is which.
- **Restore is invisible until it is needed.** It appears only on the page after closing, so nobody
  meets it before the day something goes wrong — which is the day to have already practised it.
- **A checkbook that will not open is the worst served**, and it is the main reason to restore.

What this slice is for: press **Restore a backup**, pick one from a list, confirm, and be looking at
the restored register — with the file that was replaced kept, named, and reachable.

## Decisions

| Question | Taken |
| --- | --- |
| How far restore goes | One press: replace and reopen, after a confirmation (RG-3) |
| Issue #4, the folder-scoped store | A slice of it now: the program owns `<dir>/backups/` and writes there. A fixed checkbook name, `OpenFolder` and path confinement stay in issue #4 |
| Restore against import | Settled below; `SPECIFICATION.md` amended, an explanation and a glossary added |
| Where it lives | A page reachable from the sidebar, `GET /checkbook/restore`, beside **Back up now** |

---

## 1. Restore and import are different verbs

Issue #3 says the vocabulary is muddled and should be settled "before more of the UI is built on
it." This is that settlement.

**Restore acts on files. Import acts on records.**

| | Restore | Import |
| --- | --- | --- |
| Unit | A whole checkbook file | A set of transactions |
| Source | A backup, or a checkbook, this program wrote | QIF, OFX/QFX, CSV — a foreign format (IE-1) |
| Effect | **Replaces**: you get the records in the file | **Adds**: records join what is there |
| Needs a checkbook open | No — it produces one | Yes — it puts records into one |
| Review before committing | The *file* is reviewed, by opening it read-only | Mandatory: found, duplicates, errors (IE-5) |
| Repeatable | Yes — restoring twice gives the same file | Yes, and must not duplicate (IE-6) |
| Governed by | BK-4, BK-5, BK-6 | IE-1 … IE-8 |

Two cases the pair does not obviously cover:

- **An old backup coming forward** — issue #3's "arguably an import of a past version of ourselves."
  It is neither verb. It is *migration*, an implementation detail of restore, and `Restore` is
  deliberately the only place it happens. **Restore is where backward compatibility lives.**
- **Lifting some records out of a backup into the checkbook you are working in.** Not built, and not
  a restore. If it is ever built it is an **import**: the source happens to be our own format, but
  the unit is records and the effect is additive, so IE-5, IE-6 and IE-7 apply in full. Restore must
  never grow a "merge" option (RC-1, SC-4).

### Where this gets written down

1. `SPECIFICATION.md`, since that is where arguments get adjudicated:
   - **BK-7** Restore acts on a whole checkbook file. It MUST NOT merge records, MUST NOT alter the
     file it restores from, and MUST keep any file it displaces.
   - **IE-9** Recovering individual records from a backup is an import, not a restore, and is
     subject to IE-5, IE-6 and IE-7.
   - **ST-10** The application MUST NOT create a directory as a side effect of any operation. It MAY
     create a directory whose name the program itself chose, inside a directory that already holds
     the household's records, in answer to an explicit action, and MUST say that it did so.
     ST-10 is what licenses `backups/` below; ST-6 stays about opening a database.
2. `docs/explanations/restore-and-import.md` — why there are two words for "get my records back",
   which one the reader wants, and why there is no merge.
3. `docs/references/glossary.md` — the words the interface uses: checkbook, register, backup,
   restore, import, export, cleared, reconcile, split, transfer, read-only, demo, ephemeral. Words
   for things that do not exist yet are marked as not built.
4. Issue #3's second point is answered with a link to the explanation; the rest of that issue stays
   open.

---

## 2. The folder slice

```
~/Documents/checkbook/
    checkbook.db                            the checkbook
    backups/
        checkbook-20260902-141530.db        where Back up now writes from now on
    checkbook-20260815-100000.db            older backups: still found, still listed, not moved
    checkbook-replaced-20260902-153104.db   a checkbook a restore displaced
```

- **`Back up now` writes into `<dir>/backups/`**, creating that one directory if it is missing —
  licensed by ST-10, and the page already names the file it wrote, so the move is announced rather
  than silent. `backup.Create` learns none of this: the `dir` argument stays what it is, and
  `backup.Folder(checkbook)` is the one place the convention is spelled.
- **Nothing is moved.** Backups already sitting beside the checkbook keep working, and the list
  reads both places.
- **A file is a backup because its header says so** (BK-6, last sentence). The folder is where we
  *look*; `storage.ApplicationID` is what *decides*. Listing must never infer from a name or a path.

Recorded so it is not re-argued from scratch: the alternative is to read `backups/` and never write
it, leaving **Back up now** where it is. The case for that is that BK-6 makes location meaningless,
and that relocating a household's backups on the first press after an upgrade is the kind of quiet
change this program avoids. It was considered and not taken; ST-10, plus the notice that names the
file that was written, are the answer to it.

---

## 3. What the reader does

**Sidebar**, in the **This checkbook** section, between **Back up now** and **Close checkbook**.
Withheld when the register is ephemeral, when a backup is open, and when there is no `Opener`.

**`GET /checkbook/restore`** — the list, served whether or not a checkbook is open.

```
Restore a backup

  Your records will be replaced by the ones in the backup you choose. The checkbook you have
  now is kept, under a new name, so nothing is lost either way.

  Backups in ~/Documents/checkbook
  ( ) 2 Sep 2026, 14:15    1.2 MB   backups/checkbook-20260902-141530.db
  ( ) 30 Aug 2026, 09:12   1.2 MB   backups/checkbook-20260830-091204.db
  ( ) 15 Aug 2026, 10:00   1.1 MB   checkbook-20260815-100000.db
  [ Restore this backup ]

  Somewhere else?   [ path ......................... ]   [ Restore ]

  Or restore to a different file, without replacing what you have:
  [ from ............... ]  [ to ................. ]  [ Restore to a copy ]
```

Newest first by modification time. The date shown is the stamp in the name when the name is one this
program wrote, and the file's modification time otherwise; the page says which it is showing. That
keeps the page free of instants — `nameLayout` is already local calendar time by construction — so
RG-5's "the browser converts" rule is not dragged into this slice.

**The confirmation** (RG-3) carries the checkbook's generation exactly as the close form does (CO-3):

```
Restore the backup taken 2 Sep 2026 at 14:15?

  This replaces the records in ~/Documents/checkbook/checkbook.db with the ones in
  backups/checkbook-20260902-141530.db. Anything entered since that backup was taken is not
  in it, and there is no way to merge the two.

  The checkbook you have now is kept as checkbook-replaced-20260902-153104.db, in the same
  folder. Nothing is deleted, and the backup itself is not altered.

  [ Restore it ]   [ Keep what I have ]
```

**`POST /checkbook/restore`** does the work and lands on the register with a notice naming both
files: what was restored, and what the displaced checkbook is now called.

**With nothing open** — including a `checkbook.db` that will not open — the same list and the same
one press work, against *the checkbook path* below.

**With the demo or a backup open**, the one-press restore is withheld and the page says why. The
copy form below it still works.

---

## 4. Three prerequisites

Each is a bug in its own right, and each has to land before the swap, or the swap gets written
against a relative path and an unreachable route.

**The emergency case cannot reach any route.** When `openStore` fails, `main` serves
`web.NewProblem(...)` rather than a `*web.Server`, and that is a separate mux whose `/` answers
**every** address with one static 503 page. A household whose checkbook is corrupt would get a page
telling them to restore and no address at which to do it. The fix is to build the real `Server` with
`Store: nil` and hand it the startup failure through a new `Options.Problem`, which
`renderNoCheckbookPage` already accepts. `NewProblem` then has no caller left. This is CLAUDE.md's
own rule — "the no-checkbook page reuses `DescribeOpenError`, not `NewProblem`" — finally applied to
startup.

**`-db` is never made absolute.** `main` passes the flag through raw, so `Store.Path()` is
`checkbook.db` and `filepath.Dir` of it is `.` — a directory that depends on a working directory the
reader cannot see. `browserOpener` already calls `filepath.Abs`; startup must too. It also fixes the
relative path BK-3 shows in the footer.

**There is no "the checkbook path".** `closedPath` is set by `retire` for *any* checkbook, including
a backup opened read-only. Add `writablePath`, set in `adopt` when the adopted store is neither
read-only nor in-memory, seeded from a new `Options.CheckbookPath` that `main` fills from the
now-absolute `-db`. `Server.checkbookPath()` answers with the current checkbook's path when it is
writable and on disk, and `writablePath` otherwise.

---

## 5. The swap

### Restore first, close second

The obvious order — close, restore, move aside, rename in — is wrong. `backup.Restore` reads its
source on a read-only connection of its own and writes only inside the destination's directory. It
never touches the checkbook, so it does not need it closed. Doing the long, failure-prone step
**while the register is still open and serving** buys three things:

- a bad backup, a schema from a newer release, a full disk or an unwritable folder is refused **with
  the checkbook still open** — there is no recovery path to write, and none to test;
- it is a free write-probe on the directory, using the real work rather than a probe file;
- the window in which every tab shows 503 shrinks from `close + VACUUM + two renames + open` to
  `close + two renames + open`.

### Names

`C` is the checkbook, absolute, and `dir` is its directory. `B` is the chosen backup.
`R` is `dir/checkbook-restored-<stamp>.db` and `K` is `dir/checkbook-replaced-<stamp>.db`, **with
the same stamp**, so a folder interrupted mid-swap is legible to somebody reading it. `R` must live
in `dir` so that putting it in place is a same-directory rename and never a cross-device copy.

### The sequence

| | Step | On failure |
| --- | --- | --- |
| 0 | Validate: `C` non-empty and not in memory; `B` is one of the paths the listing just returned; `B` is not `C`; the header of `B` is `AppID` or `BackupAppID`; a cheap generation pre-check | Refuse. Nothing done |
| 1 | Pick free names `R` and `K` | `ErrNameInUse`. Nothing done |
| 2 | `backup.Restore(ctx, B, R)` — **the checkbook is still open and serving** | `Restore` has removed its own working copy. Reuse the existing refusal branches, reworded to end "your checkbook is still open and nothing about it has changed" |
| 3 | Take `ctl`; `retire(gen)`; `closeRetired(cb)` **synchronously** | Stale generation → 409, nothing moved; the page names `R` and offers to open it |
| 4a | Rename `C-shm` to `K-shm` if it is there | Remove it instead; if that fails, abort and reopen `C` |
| 4b | Rename `C-wal` to `K-wal` if it is there | Undo 4a, reopen `C` |
| 4c | Rename `C` to `K` — **the window opens** | **Undo 4b, then 4a**, then reopen `C`. If the undo fails, do **not** reopen: report, naming `C` and `K-wal` |
| 5 | Assert `C` is absent, then rename `R` to `C` — the window closes | Move `K` and its sidecars back, reopen `C`, and report that `R` is still there |
| 6 | Open `C` through the `Opener`, adopt it, release `ctl` | The swap already succeeded — do **not** roll back. `DescribeOpenError` on the no-checkbook page, naming `C` **and** `K` |

Compensation is a strict mirror of the forward path. That symmetry is what makes it reviewable.

### Five rules the sequence encodes

**The sidecars move with the database, and they move back in reverse.** SQLite binds a WAL to its
database by filename, and nothing inside a WAL records a path, so a `.db` and its `-wal` renamed
together recover exactly as they would have in place. The bug to avoid is 4b succeeding and 4c
failing: `C` would still hold the database while `C-wal`'s committed frames now sat at `K-wal`, and
reopening `C` would **silently lose every transaction in that WAL** — the quiet loss CO-3 forbids,
arriving through a filesystem instead of an `UPDATE`. `restore.go` already carries this argument in
prose; quote it rather than deriving it again. A `-shm` holds no records and may be removed if it
will not move. A `-wal` never may.

**`os.Rename` replaces an existing file on both platforms.** The assertion that `C` is absent before
step 5 is not paranoia: it is what catches a second swap racing this one.

**`ctl` is held across steps 3 to 6 as one critical section.** Between 4c and 5 there is no file at
`C`. A `POST /checkbook/open` for `C` in that window would have `storage.Open` **create** an empty
checkbook, migrate it, and adopt it — and then step 5 would rename a file over it, leaving a live
pool on an unlinked inode and a household typing into nothing. `ctl` is never taken by a request
that reads the register, and `handleOpen` already holds it for its whole body, so this blocks only
other control actions. Step 2 stays **outside** `ctl`: a `VACUUM` must not block Close or Quit.

**The context is detached from step 3 onward.** A reader who closes the tab mid-swap must not cancel
the reopen and leave the program with nothing open.

**Bounded retry on 4c, on 5, and on 5's compensation** — five attempts, about 100ms apart. On
Windows a file with an open handle cannot be renamed, and antivirus, Windows Search and OneDrive
hold files transiently. It is ten lines, and on Unix the loop never runs twice.

### Two things deliberately not done

**No `backup.Create` of `C` before the swap.** `VACUUM INTO` on a damaged database fails, so
requiring a backup first would block exactly the emergency this feature exists for. **BK-1 is
satisfied by the move-aside**: `K` is a timestamped copy of the pre-operation state, kept, complete
with its WAL, and — carrying `AppID` rather than `BackupAppID` — openable directly, which is a
shorter road back than a file that must itself be restored. Say that in a sentence in the code,
because a reviewer will otherwise reach for BK-1.

**No hard link before the rename.** Linking `C` to `K` and then renaming `R` over `C` would make
"no file at `C`" literally impossible on Unix. It fails on FAT and exFAT — the USB stick a household
keeps records on — and on Windows it is same-volume NTFS only, so PL-5's "the same behavior on both
platforms" goes. Recorded here so it is not proposed again.

### The residual risk, stated plainly

Power loss between 4c and 5 leaves `K` and `R` and nothing at `C`, and the next start would create a
fresh empty checkbook on top of an emergency. The window is narrow — 4c succeeding is itself the
evidence that the directory is writable and unlocked, and 5 follows microseconds later — and it is
mitigated by the shared stamp in the two names and by the how-to explaining what that pair means. A
later change might have the page for a brand-new empty checkbook notice a `checkbook-replaced-*`
beside it and say so. That is not in this slice.

---

## 6. Where the code goes

### `internal/backup` — domain, no `net/http` (TS-2), so the TUI gets it unchanged

```go
type Backup struct {
    Path     string    // absolute
    Taken    time.Time // modification time; the displayed stamp comes from the name when we wrote it
    Bytes    int64
    IsBackup bool      // false for a checkbook that could still be restored from
}

func Folder(checkbook string) string          // <dir>/backups -- the one place the convention lives
func Find(dir string) ([]Backup, error)       // dir and dir/backups, newest first
func Replace(restored, checkbook string) (kept string, err error)
```

`Replace` belongs here firmly: file mechanics with no HTTP in them, reusing the `freeName`,
`tempName` and sidecar reasoning already in `backup.go`. **No `ctx`** — three renames, nothing
cancellable, and a context accepted and ignored is a lie this codebase does not tell.

Its contract:

- The caller must have closed the checkbook. On Windows the rename failing is the assertion; on Unix
  there is nothing to assert. Document the precondition the way `Restore` documents its own.
- **An absent checkbook is not an error.** That is the case where `-db` never opened: `kept` comes
  back empty and `restored` is put in place.
- Check the header of `restored` before anything moves. It is the last gate before a file takes the
  household's checkbook name.
- Sentinels: `ErrCheckbookNotMoved` (step 4 failed, the original is intact), `ErrNotPutInPlace`
  (step 5 failed, the original is back), and `ErrCheckbookDisplaced` — both failed, the one
  unacceptable outcome, which earns its own sentinel so the browser can answer it differently from
  every other failure.
- Export `ValidReplacedName`, the same rule and the same reason as `validBackupName`: the browser
  validates a filename before putting it in a sentence.

`Find` skips `-wal`, `-shm` and `.checkbook-*.tmp` **by name, before opening them** — a concurrent
restore's working copy should not be opened at all. It skips directories, dedupes by cleaned
absolute path because `dir/backups` may be a symlink to `dir`, never descends, and stats and sorts
before confirming the newest fifty by header, since reading an application id opens a connection per
file.

### `internal/web`

| Route | Meaning | Registered |
| --- | --- | --- |
| `GET /checkbook/restore` | The list | always |
| `GET /checkbook/restore/confirm` | The confirmation (RG-3), carrying the generation | always |
| `POST /checkbook/restore` | The one-press restore-and-use | **only when there is an `Opener`** |
| `POST /checkbook/restore/copy` | Today's two-box restore, behavior unchanged | always |

The one-press route has to reopen, so it needs an `Opener`. The copy route does not, which is how
today's rule that restoring is always offered survives. The existing handler, `RestoreRequest`, the
eight refusal branches, `suggestName` and `restoredCheckbookExists` all move to `/copy`
**unchanged**; only the form's `action` changes.

`GET /checkbook/restore` must **not** be wrapped in `withCheckbook`. That answers 503 when nothing
is open, and nothing-open is the case the feature exists for. Register it plainly, the way
`handleCheckbook` is registered, and render through `pageLayout` with `currentCheckbook()`, which
already accepts nil.

Dates and sizes are formatted in Go, never in a template.

---

## 7. The files, and the order

| File | Change |
| --- | --- |
| `cmd/checkbook/main.go` | `-db` made absolute; serve the real `Server` with `Options.Problem` on a failed open; pass `Options.CheckbookPath` |
| `internal/web/problem.go` | `NewProblem` retired; `DescribeOpenError` stays |
| `internal/web/server.go` | `Options.Problem`, `Options.CheckbookPath`; `writablePath`; `checkbookPath()`; the four routes |
| `internal/web/checkbook.go` | `adopt` records `writablePath` for a writable on-disk store |
| `internal/web/restore.go` | The existing handler moves to `/checkbook/restore/copy`; the list, confirm and swap handlers are new |
| `internal/web/nocheckbook.go` | The list replaces the two boxes as the primary offer; the copy form stays below it |
| `internal/web/backup.go` | **Back up now** writes into `backup.Folder(path)`, creating it (ST-10) and saying so |
| `internal/backup/find.go`, `internal/backup/replace.go` | New: `Folder`, `Find`, `Replace`, and their sentinels |
| `internal/web/templates/` | New `restore.gohtml` and `restore-confirm.gohtml`; the sidebar entry in `layout.gohtml`; `no-checkbook.gohtml` reworked |
| `SPECIFICATION.md` | BK-7, IE-9, ST-10 |
| `docs/explanations/restore-and-import.md`, `docs/references/glossary.md` | New |
| `docs/how-to/restore-a-backup.md` | Rewritten for the new flow. It is **also stale today**: step 1 still says a backup is refused unless the box is ticked, which changed in 0.20.2-beta |
| `docs/references/user-manual.md` | The restore section, where backups live, the sidebar table, the routes table, the status table, the version line |
| `CLAUDE.md` | A Restore paragraph beside Backups |
| `version.go` | A **minor** bump: new user-visible capability |

Order:

1. `-db` made absolute. Isolated, and it fixes the footer.
2. A failed startup serves the real `Server`. Pinned by `TestARegisterThatWouldNotOpenStillOffersRestore`.
3. `checkbookPath()` and `Options.CheckbookPath`. No UI yet.
4. `backup.Folder` and `backup.Find`, with tests. A pure addition.
5. `backup.Replace`, with tests, including the WAL-move-back case. A pure addition that nothing calls
   yet — this is where the sidecar ordering and the retry loop get pinned against a real filesystem
   before any HTTP is involved.
6. The list and confirm pages. Read-only.
7. The swap, with `ctl` held across steps 3 to 6 and the detached context, then the browser tests.
8. The sidebar entry and the no-checkbook page, withheld for the demo, for a backup, and for a build
   with no `Opener`.
9. **Back up now** writing into `backups/`.
10. The specification, the docs, the manual, CLAUDE.md, and the version.

---

## 8. What has to be pinned

The house style: `package backup_test` and `package web_test`, table tests where there are several
shapes, prose comments naming the rule **and why it exists**, and an assertion on every failure path
that nothing was lost.

**`internal/backup/find_test.go`** — `TestFindListsBackupsNewestFirst`;
`TestFindConfirmsByHeaderNotByName`, where a backup renamed `notes.txt` is found and a text file
named `checkbook-20260101-000000.db` is not (BK-6); `TestFindLooksInBothPlaces`;
`TestFindDoesNotMindAMissingBackupsFolder`, which also creates nothing;
`TestFindSkipsSidecarsAndWorkingCopies`; `TestFindSkipsDirectories`.

**`internal/backup/replace_test.go`** — `TestReplacePutsTheRestoredFileInPlaceAndKeepsTheOld`;
`TestReplaceMovesTheWalWithTheDatabase`; `TestReplaceLeavesNoSidecarBesideTheNewCheckbook`;
`TestReplaceWithNoCheckbookThere`, the emergency; `TestReplaceNeverDeletesAnything`, hashing every
file before and after so that the only thing that changed is names;
`TestReplacePutsTheOriginalBackWhenItCannotFinish`;
**`TestReplacePutsTheWalBackWhenTheAsideCannotFinish`**, the silent-loss bug above, which must have
a test of its own; `TestReplaceRefusesWhenTheAsideCannotBeMade`;
`TestReplaceSaysSoWhenNothingIsAtTheCheckbooksName`, asserting `ErrCheckbookDisplaced` and that the
message names both files; `TestReplaceRefusesAFileThatIsNotACheckbook`;
`TestReplaceRefusesToPutAFileOverOne`.

**`internal/web/restore_test.go`** — `TestRestorePageListsTheBackupsItFound`;
**`TestARegisterThatWouldNotOpenStillOffersRestore`**, which forces the first prerequisite and
without which the feature ships unreachable; `TestRestorePageWorksWithNothingOpen`;
`TestRestoreAsksBeforeReplacing`, which names both files, offers a way out, and leaves the register
answering; `TestRestoreSwapsTheCheckbookInOnePress`; `TestRestoreKeepsTheDisplacedCheckbook`, where
the kept file is **named on the page the reader lands on**;
**`TestAFailedRestoreLeavesTheCheckbookOpen`**, which is what restore-first buys and must not
regress; `TestStaleRestoreIsRefused` (CO-3, shaped like `TestStaleCloseIsRefused`);
`TestRestoreRefusesABackupItDidNotOffer`; `TestRestoreRefusesTheDemo`;
`TestRestoreIsWithheldWithoutAnOpener`; `TestRestoreUnderLoad` under `-race`, shaped like
`TestCloseUnderLoad`, ending with **exactly one file at the checkbook's name, and it opens**;
`TestTwoRestoresAtOnce`; **`TestAnOpenDuringTheSwapCannotCreateAnEmptyCheckbook`**, the race that
justifies holding `ctl`; and `TestTheCopyRestoreStillWorks`.

By hand, against the running program:

1. Back up, and confirm the file lands in `backups/` and the page says so.
2. Enter a transaction, restore the backup, and confirm the register loses it while
   `checkbook-replaced-*.db` sits beside the database and opens with the transaction still in it.
3. Corrupt a copy of `checkbook.db`, start the program on it, and confirm the no-checkbook page
   comes up **with the restore list on it** and that one press recovers.
4. Leave a `checkbook.db-wal` behind by killing the process mid-write, restore, and confirm the WAL
   moved with the displaced file and that no `-wal` sits beside the new checkbook.
5. Two tabs: confirm in one, restore in the other, then press the stale confirmation. 409, and
   nothing swapped twice.
6. `go vet ./... && gofmt -l . && go test ./... -race`.
