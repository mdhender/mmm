# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

`mmm` is a local-first household **checkbook** written in Go. The repository is early. The
storage layer, the domain packages behind the register, and a web UI exist (`cmd/checkbook` serves
it). The register creates accounts, displays them, takes new transactions, and marks them cleared;
editing an account or a transaction, splitting, transfers, importing, and reconciling do not exist
yet. Much of the code written here is still creating the application rather than modifying it.

`SPECIFICATION.md` is the binding document: numbered, checkable requirements (`PL-`, `ST-`,
`RG-`, `RC-`, `IE-`, `BK-`, `CO-`, `PV-`, `TS-`, `RP-`, `SC-`). Check work against it and cite IDs
in commits and reviews. `MANIFESTO.md` explains why those rules exist and settles questions of
taste and direction. The notes directly under `docs/` are design intent for specific slices
(storage, imports, reconciliation, web UI, TUI, package layout) and are **not** binding — where
they conflict with the specification, the specification wins.

`docs/references/`, `docs/how-to/`, and `docs/explanations/` are something else: **end-user
documentation**, organized by [Diataxis](https://diataxis.fr) type. They describe the program as
it actually behaves, so a change in behavior is a change to them — particularly
`docs/references/user-manual.md`, which names the version it applies to. Do not document intent
there; a feature that does not exist yet is listed as absent, not described as present.

## Commands

```sh
go build ./...                          # compile everything (no binary left behind)
go run ./cmd/checkbook                  # run the web UI
go run ./cmd/checkbook -demo            # ... over sample data held in memory, touching no files
go run ./cmd/checkbook-tui              # run the terminal register (not built yet)
go test ./...                           # all tests
go test -run TestName ./internal/pkg    # single test
go vet ./...
gofmt -l .                              # list unformatted files
```

**Always run commands as `go run ./cmd/foo` from the repository root** — do not
`go build -o some-binary` during development. Two reasons: it keeps stray executables out of the
repository root, and it forces the habit of running from the root, which keeps relative data paths
(the database, `.env` files, `imports/`) consistent. That last point matters most on Windows,
where a binary launched from another directory resolves relative paths somewhere unexpected.
Building a named executable is for producing a release artifact, not for day-to-day work.

Go toolchain is 1.26.4 (`go.mod`). Dependencies: `github.com/maloquacious/semver` (version
string), `github.com/joho/godotenv` (wrapped by `internal/dotenv`), and
`zombiezen.com/go/sqlite` (storage).

**No Apache-2.0 dependencies.** The project is MIT, and every module in the tree today is MIT,
BSD-3, or ISC — there is no Apache-2.0 anywhere, directly or transitively, and it stays that way.
Check the license before adding a module, not after. This rules out some otherwise obvious
choices, `github.com/inconshreveable/mousetrap` among them; where the wanted code is a handful of
stdlib `syscall` calls, write it here instead of taking the dependency (TS-4 would usually say
that anyway).

## Architecture (intended)

```
browser (HTML + HTMX)  ──localhost──▶  net/http + html/template
                                              │
                        ┌─────────────────────┴──────────────┐
                     domain model                     import/export
                        └─────────────────────┬──────────────┘
                                          SQLite (checkbook.db)
```

Planned layout (`docs/project-structure.md`):

```
internal/{account,transaction,reconciliation,import,storage}
cmd/checkbook       # local web UI: HTTP server bound to 127.0.0.1, opens default browser
cmd/checkbook-tui   # terminal register, same DB and same domain packages
```

The domain packages must stay independent of the browser interface so the TUI and any future
CLI subcommands (`checkbook import statement.qfx`, `checkbook export --ledger`, `checkbook backup`)
reuse them rather than becoming a second application.

### Existing packages

- `internal/cerrs` — `cerrs.Error` is a `string` type implementing `error`, so sentinel errors are
  untyped constants: `const ErrNotFound = cerrs.Error("not found")`. Declare new sentinels this way
  rather than with `errors.New`.
- `internal/money` — exact monetary values (CO-1). `Money` pairs an `int64` count of **minor
  units** with a `Currency`; construct with `money.ParseDecimal("43.21", money.USD)` or
  `money.Zero(cur)`, read back with `.Amount()`, `.Currency()`, `.Decimal()`. Currencies carry
  their own exponent (JPY 0, USD 2, KWD 3). Arithmetic exists both as methods and as free
  functions in `functions.go`; every cross-value operation returns an `error` because mixing
  currencies is rejected rather than coerced. Never introduce a parallel float or `int` amount
  type — use this one. See `internal/money/README.md`.
- `internal/account`, `internal/category`, `internal/transaction` — the domain packages behind the
  register. They take a `*storage.Store`, return domain types, and import nothing from `net/http`
  (TS-2), so the planned TUI and CLI use the same code. `transaction.LoadRegister(ctx, store,
  acct)` is the register itself: entries ordered by date then id, each carrying its category, its
  split count, and the running balance, plus the ending and cleared balances. **The running
  balance is computed there, not in a template**, so every interface shows the same number.
  `transaction.Create` writes a transaction and its splits in one database transaction and
  rejects splits that do not total the amount.
- `internal/web` — the browser interface; see Web UI below.
- `internal/storage` — opens and migrates the SQLite database; owns the schema. `storage.Open(ctx,
  path)` returns a `*Store`; borrow connections with `Conn(ctx)` and always return them with
  `Put`. **`Pool.Close` blocks until every borrowed connection is returned** — a `defer
  store.Close()` alongside a still-borrowed connection deadlocks. `OpenMemory` gives tests a
  database that touches no files. Also exports `BindMoney` / `ColumnMoney`; see Storage below.
- `internal/dotenv` — `dotenv.Load(env)` wraps `joho/godotenv`. `env` must be one of
  `development`, `test`, `production`, or `agents`; **`agents` is reserved for the coding agent's
  own local work**. Files load highest-precedence first: `.env.{env}.local`, `.env.local`,
  `.env.{env}`, `.env`. The `.local` files are gitignored and are the only ones that may hold
  secrets; `.env` and `.env.{env}` are committed and must never contain any.

### Storage

`internal/storage` is the only package that talks to SQLite. It uses **ZombieZen**
(`zombiezen.com/go/sqlite`), which is deliberately *not* a `database/sql` driver — there is no
`Valuer`/`Scanner` hook, so every bind and read is explicit against a closed set of primitives
(`SetInt64`, `GetInt64`, `ColumnText`, …).

Schema changes go through `zombiezen.com/go/sqlite/sqlitemigration` in `schema.go`. Never
hand-roll migration logic:

- `AppID` is `0x4d4d4d20`, the ASCII bytes of `"MMM "`. It is written to the file header, and
  `sqlitemigration` refuses to migrate a database whose `application_id` differs — the guard that
  stops the program writing into an unrelated SQLite file. **It must never change.**
- Migrations are **append-only** (ST-4). Progress is a count in `user_version`, so editing or
  reordering a shipped migration silently desynchronizes every database that already ran it.
  Correct a mistake by appending a migration. `MigrationCount()` reports how many there are;
  assert against it rather than hardcoding a number.
- Some changes need a **table rebuild** (create, copy, drop, rename) because SQLite's `ALTER TABLE`
  cannot add `AUTOINCREMENT`, a CHECK, or a non-constant DEFAULT. Migration 3 is the worked
  example: it sets `legacy_alter_table` for the duration and declares
  `MigrationOptions{DisableForeignKeys: true}`. Note that a non-constant `DEFAULT` *is* legal in
  `CREATE TABLE` and only forbidden in `ADD COLUMN`.
- `prepareConn` sets two pragmas per connection: `foreign_keys = ON` (SQLite defaults it **off**,
  which would make every `REFERENCES` clause decorative) and `journal_mode = WAL`.

Conventions the schema encodes:

- **Money is `INTEGER` minor units**, never `REAL` (CO-1). Bind with `storage.BindMoney`, read with
  `storage.ColumnMoney`. Every monetary column carries `CHECK (typeof(col) = 'integer')`, because
  column affinity would otherwise accept a float or string and destroy exactness silently.
- **Never pass a `money.Money` to `sqlitex.ExecOptions.Args`.** Args falls back to `fmt.Sprint` for
  unknown kinds, and `Money` has a `String` method, so the bind *succeeds* and stores the text
  `"USD 125.95"`; reading it back as an integer yields `0`. The `typeof()` CHECKs exist to catch
  this. Never store an amount as JSON either — it cannot be summed or indexed.
- **Currency lives on the account**, not on each amount, so `SUM(amount)` over a register is
  meaningful. `ColumnMoney` therefore takes the currency from the caller.
- **Dates are `TEXT` in ISO 8601 `YYYY-MM-DD`**, which sorts chronologically as a string. A `GLOB`
  CHECK enforces the shape; impossible-but-well-formed dates are the domain layer's problem.

### Testing against a database

Use `storage.OpenMemory(ctx, name)`. It touches no files and vanishes on `Close`.

ZombieZen rejects a bare `":memory:"` outright, and the reason is worth knowing: with a pool,
each connection would silently get *its own separate database*. So the mode is chosen by name:

- **Shared** (`name` non-empty) — one database behind the whole pool, so the Store behaves like
  the file-backed one. The name is process-wide: two tests sharing a name share a database. Pass
  something unique. `t.Name()` works, but **a subtest's name contains `/`**, which is rejected —
  sanitize it (`strings.ReplaceAll(t.Name(), "/", "-")`). Names are restricted to letters, digits,
  `-`, `_`, and `.` so a name can never inject a URI parameter and quietly turn the database into
  a file on disk.
- **Private** (`name` empty) — cannot be shared between connections at all, so the pool holds
  exactly **one**. A second concurrent borrow blocks until the first is returned. Use it when the
  test never needs concurrency; it cannot collide with anything.

In-memory databases do not support WAL and report a journal mode of `memory`. `prepareConn`
expects that for in-memory stores and skips the WAL check, but still enforces and verifies
`foreign_keys`, so constraint behavior under test matches production.

### Time, dates, and identifiers

**Instants are stored in UTC** as `YYYY-MM-DDTHH:MM:SS.ffffffZ` (ST-7) — `storage.TimeLayout`,
with `FormatTime`, `ParseTime`, `BindTime`, and `ColumnTime`. One timezone and a fixed width are
what make the TEXT column sort chronologically. Parsing is strict: a value with an offset other
than `Z`, or without microseconds, is rejected rather than quietly reinterpreted. **The browser
converts for display** (RG-5); the server never assumes a household timezone.

**Calendar dates are not instants** (ST-8). `transactions.date` and `statement_date` stay
`YYYY-MM-DD` and are deliberately timezone-free — converting them to UTC would let a purchase move
to the previous day for a household west of UTC. Do not "fix" this by making them timestamps.

**Every table uses `INTEGER PRIMARY KEY AUTOINCREMENT`** (ST-9). Without it SQLite assigns
`max(rowid)+1`, so deleting the newest row hands its id to the next insert and any tab, bookmark,
export, or reconciliation holding that id silently comes to mean a different record. `AUTOINCREMENT`
keeps a high-water mark in `sqlite_sequence`. New tables must use it too.

### Concurrency and the multi-tab case

The household may have several browser tabs open on the same register, so requests genuinely
overlap. `PoolSize` is 10 and set explicitly.

`prepareConn` **verifies** rather than merely requests its pragmas — it reads `foreign_keys` and
`journal_mode` back and returns an error if either did not take. sqlitex calls it lazily, once per
connection on that connection's first `Take`, and returns the error instead of the connection, so
every connection a caller can borrow has been through it. A test borrows all ten at once and
checks each; do not weaken that to a single-connection check.

WAL arrives from sqlitex's default `Flags` (which include `sqlite.OpenWAL`). **Setting
`Options.Flags` explicitly would silently drop it** — the verification in `prepareConn` is what
turns that into a startup failure rather than a quiet loss of crash safety.

**What WAL does and does not solve.** It keeps readers and writers from blocking each other: one
tab can scroll the register while another imports. It does *nothing* about lost updates. Two tabs
that load the same transaction and both save will have the second write silently discard the
first — WAL, connection pooling, and transactions all permit this, because each request is its own
short transaction.

CO-3 forbids that. **`transaction.SetStatus` is the worked example** — marking a row cleared reads
the token with the row, sends it back with the change, and refuses a write whose token has moved
on. The token is `updated_at`, not a separate counter: read it with the record, then
`UPDATE ... SET updated_at = ? WHERE id = ? AND updated_at = ?`. A stale tab matches no rows, so
`Changes()` returns 0 and the caller must tell the user rather than retrying blindly.

Always compute the new value with `storage.NextUpdatedAt(prev, now)`. A wall clock alone is not a
safe token — two edits inside one microsecond, or a clock stepped backwards by NTP, would reuse a
value and let the *next* compare-and-set succeed against stale data. `NextUpdatedAt` returns the
later of `now` and one microsecond past `prev`, so the token strictly increases per record while
staying an honest timestamp.

`splits` deliberately have no token. A split is edited as part of its transaction, so the
transaction is the aggregate root and its `updated_at` guards the whole record.

**No authentication, authorization, sessions, or CSRF tokens** (PL-7). The server binds to
loopback, has no remote origin and no notion of accounts, so there is no second principal for such
a mechanism to distinguish. Do not add this machinery reflexively because the code looks like a
web application.

`Open` never creates directories (ST-6): a path whose parent is missing fails with
`ErrMissingDirectory` and leaves the filesystem untouched, so a stray relative path in a test
cannot scatter folders across the tree.

`Open` also **verifies `user_version` against `MigrationCount()`** and refuses a mismatch
(`ErrDatabaseTooNew`, `ErrSchemaVersion`). Do not remove this on the grounds that sqlitemigration
already migrates: its loop runs only while `user_version` is *below* the migration count, so a
database written by a later release falls straight through it and would be opened against a schema
this build has never seen. The first symptom of that is a wrong answer, not an error.
`ErrNotCheckbook` is recognized from sqlitemigration's `application_id` message — string matching,
deliberately not load-bearing, pinned by `TestOpenRejectsForeignDatabase` so a wording change
fails a test instead of degrading a user's page.

### Holding the console open

`holdConsoleOnExit` blocks on Enter before a failing exit, and it is a **last resort, not a
general error path**. `run` returns whether it managed to open a browser; when it did, the reader
already has the explanation on screen — a database that will not open is served as a page — and
the console must not stop. Only a failure early enough that there is no listener, and so no page
and no window, has nowhere else to go. Then two conditions remain: `runtime.GOOS == "windows"`,
because a console allocated by double-clicking closes the instant the process exits while macOS
keeps a failed Terminal window and a Unix shell never closes; and stdin being a character device,
so a script driving the program is never left waiting on input.

The trigger is `startedByExplorer` (`cmd/checkbook/explorer_windows.go`), which asks whether the
parent process is `explorer.exe` by walking a `CreateToolhelp32Snapshot` — all stdlib `syscall`,
no dependency, per the licence rule above. **One snapshot, one pass**: the parent id and the
shell ids come from the same instant, and only the `explorer.exe` ids are kept rather than a table
of every process. It is conservative — every failure answers false, costing a message rather than
a program stopped for input nobody is there to give. Two documented limits: a parent that has
already exited leaves an id Windows may have reused, and launching by any other route (a
third-party file manager, a double-clicked `.bat`, "Run as administrator") reads as a command
line. Neither is worth more machinery. `explorer_other.go` returns a constant off Windows, which
is what keeps a Unix shell or a macOS Terminal from ever waiting on input.

Note also how little the hold protects. It is reached from exactly
four places, and a double-clicking user cannot reach the first two, because both require passing a
flag they never pass: a non-loopback `-host`, and a `-port` already occupied (the default of 0
asks the system for a free one). The other two are our own template bugs. Everything a household
actually hits — a bad path, a foreign file, a schema from a newer release — is served as a page in
the browser, and a browser that fails to open leaves the program *running*, so the console stays
up with the address in it regardless.

**`DefaultPort` is 8842, and fixed on purpose.** An address that changes every run cannot be
bookmarked, which leaves someone who closed the browser with no way back to a program that is
still running -- so they start it again, and again. The fixed port also makes the second start
fail to bind, which says "already open, here is where" truthfully and without asking anything.
`-port 0` still asks the system for a free one.

**`DemoPort` is 8843: `-demo` listens beside the register, not on top of it.** The demo is what
somebody opens while their own checkbook is already open, and one port cannot hold both, so the
default moves rather than making them choose. `portWasGiven` is what keeps this honest: it asks
whether `-port` was *set*, not what it holds, so `-port 8842 -demo` is obeyed. `portInUse` also
skips 8843 when it suggests another port, since advice that lands on a running demo fails twice.

**Running two copies at once is allowed, including on one database** -- the second just needs its
own `-port`. Do not add a lock for it.
SQLite coordinates multi-process access, and CO-3 is enforced by the `updated_at` token in the
schema, which does not care whether the competing writer is another connection, another tab, or
another process. Two copies are the two-tab case the project already supports by design.

### When the database will not open

`cmd/checkbook` does not exit when `openStore` fails. It builds `web.NewProblem` from
`web.DescribeOpenError` and serves that one page at **every** address with a 503, opens the
browser on it, and exits 1 once stopped. The reason is PL-4's own premise: the program is started
by double-clicking it, so a message printed to a terminal nobody is looking at is not a message.
Only failures that prevent listening at all (a non-loopback `-host`, a port in use) still exit
early, because there is then no way to serve the page.

`DescribeOpenError` matches on **sentinels, not message text**, so a reworded underlying error
cannot silently turn a specific page into a vague one; the unrecognized case still carries next
steps. `problem.gohtml` is standalone and deliberately not built on `layout.gohtml` — the layout's
frame is the account list, and there is no database to read one from.

### Web UI

`cmd/checkbook` opens a listener, opens the database, and serves `internal/web`. It **refuses a
non-loopback `-host`** rather than trusting the flag: the register has no authentication by
design (PL-7), which is only safe because it is unreachable from off the machine (PL-4). Only
literal IPs and the name `localhost` are accepted, because resolving anything else could mean a
DNS query and the program must run without a network (PL-3). `-port` defaults to 0, so the system
picks a free port and the URL is printed; `-demo` serves a sample household from an in-memory
store and writes nothing to disk.

Shutdown order matters: `srv.Shutdown` runs **before** the store is closed, because
`Pool.Close` blocks until every borrowed connection comes back and a handler still running holds
one.

`internal/web` is thin on purpose. It parses a request, asks a domain package, formats, and writes
HTML. No SQL and no balance arithmetic belong here.

- **Templates** live in `internal/web/templates` and are embedded. Each page is parsed into its
  own set — `layout.gohtml` plus that page's file — because every page defines a template named
  `main` and one set cannot hold two. A page is rendered **into a buffer first**; writing straight
  to the `ResponseWriter` would commit a 200 and half a document before a template error could be
  found.
- **Formatting happens in Go, not in templates.** Handlers build a `registerPage` of ready
  strings, so the template only places values. `formatAmount` groups the digits that
  `money.Decimal()` produced and never recomputes them (CO-1).
- **Calendar dates are printed exactly as stored** (ST-8, RG-5). Nothing in the UI converts a
  transaction date. When instants do get displayed, the conversion belongs in the browser.
- **Every error page says what happened and what to do next** (RG-4). `Server.fail` takes both,
  and `errorPage.NextStep` is not optional — a page without it is a dead end. The catch-all
  `GET /` route exists so a mistyped address gets one of these instead of net/http's bare 404.
- **HTMX is vendored at `static/htmx.min.js`** (2.0.4, 0BSD; provenance and hash in
  `internal/web/htmx.md`). It arrived with marking a row cleared, the first interaction that
  genuinely replaces part of a page: the answer is the one `<tr>` plus the totals as an
  out-of-band swap, because clearing moves the cleared balance and the uncleared count but not the
  ending balance. It is **never a CDN reference** (PL-3, TS-4). Entering a transaction and
  creating an account are still plain POSTs and redirects — a new row changes the running balance
  of every row below it, and a new account changes the list beside every page, so in neither case
  is there a fragment to swap.
- **Every htmx control is a real form that works without it.** The handler answers a fragment when
  `HX-Request` is set and a redirect otherwise, so the register keeps working if the script never
  loads. Keep it that way rather than putting `hx-` attributes on a bare element.
- **`register.gohtml` defines `row`, `totals`, and `notice`**, rendered both inside the page and
  on their own. A change to a register row belongs in the `row` template, or a swapped-in row and
  a reloaded page will disagree.
- **An in-memory database is marked in the frame.** `layout.Ephemeral` comes from
  `store.IsMemory()`, not from the `-demo` flag, so the mark follows the database that keeps
  nothing rather than the way the program was started. Both bars turn amber and an hourglass is
  shown top and bottom -- a register that keeps nothing must not look like one that does. Every
  page goes through `Server.pageLayout`, which is what stops a new page quietly omitting it, or
  the database path BK-3 requires.
- **Do not add authentication, sessions, or CSRF tokens** (PL-7).

## Constraints that override normal defaults

`SPECIFICATION.md` is authoritative and should be read in full. These are the requirements most
likely to be violated by reasonable-looking default behavior, so they are repeated here as
pointers — if this list and the specification ever disagree, the specification is right:

- **Money is exact** (CO-1). Never binary floating point for amounts — integer minor units or a
  decimal type.
- **No network beyond loopback** (PL-3, PL-4, IE-2, IE-3, IE-4, PV-1, PV-2). No bank APIs, no
  aggregators, no stored credentials, no telemetry. Import is from QIF, OFX/QFX, and CSV files.
- **No JavaScript build, no framework, no Node** (PL-2, TS-1). Server-rendered HTML with small
  HTMX interactions. Per `docs/htmx-web-ui.md`, no REST API, no JSON, no client-side data model;
  register rows are table rows swapped by HTMX, with ~100 lines of CSS.
- **Domain stays UI-independent** (TS-2, TS-3) so the TUI and CLI reuse it.
- **Imports are explicit and idempotent** (IE-5, IE-6, IE-7). Show found/duplicate/error counts
  and let the user review before committing; never silently merge, categorize, or delete.
- **Reconciliation never manufactures agreement** (RC-1, RC-2, RC-3). No adjustment entries, no
  edits to prior transactions.
- **Explicit migrations, confirmed destructive actions, timestamped backups before risky
  operations** (ST-4, RG-3, BK-1).
- **No ads, nags, expiring features, or degraded old releases** (PV-3, PV-4).

For scope questions apply SC-1's three tests; SC-2 lists what is out of scope and SC-3 fixes the
v1 feature set. Prefer omitting a feature to weakening trust in the register (SC-4).

## Workflow

**Commit directly to `main`.** While the version carries the `beta` pre-release tag, do not create
branches or pull requests for ordinary work. Revisit this once the project leaves beta.

**Version bumps live in `version.go`** and are part of the change that requires them:

- Any code change bumps the **minor** or **patch** version. New packages or user-visible
  capability take the minor; fixes and small internal changes take the patch.
- A docs-only change (`*.md`, `docs/`) bumps **nothing**. Leave `version.go` alone.

**GitHub issues and PRs are assigned to the user at creation**, not edited afterwards:

```sh
gh issue create --assignee @me ...
gh pr create --assignee @me ...
```
