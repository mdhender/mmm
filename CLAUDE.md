# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repository is

`mmm` is a local-first household **checkbook** written in Go. The repository is early: it has
`MANIFESTO.md`, design notes under `docs/`, `version.go`, and two small support packages
(`internal/cerrs`, `internal/dotenv`). There is no `cmd/`, domain package, or SQLite schema yet —
code written here is largely creating the application, not modifying it.

`SPECIFICATION.md` is the binding document: numbered, checkable requirements (`PL-`, `ST-`,
`RG-`, `RC-`, `IE-`, `BK-`, `CO-`, `PV-`, `TS-`, `RP-`, `SC-`). Check work against it and cite IDs
in commits and reviews. `MANIFESTO.md` explains why those rules exist and settles questions of
taste and direction. The `docs/` notes are design intent for specific slices (storage, imports,
reconciliation, web UI, TUI, package layout) and are **not** binding — where they conflict with
the specification, the specification wins.

## Commands

```sh
go build ./...                          # compile everything (no binary left behind)
go run ./cmd/checkbook                  # run the web UI
go run ./cmd/checkbook-tui              # run the terminal register
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
- `internal/storage` — opens and migrates the SQLite database; owns the schema. `storage.Open(ctx,
  path)` returns a `*Store`; borrow connections with `Conn(ctx)` and always return them with
  `Put`. **`Pool.Close` blocks until every borrowed connection is returned** — a `defer
  store.Close()` alongside a still-borrowed connection deadlocks. Also exports `BindMoney` /
  `ColumnMoney`; see Storage below.
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
  Correct a mistake by appending a migration.
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
