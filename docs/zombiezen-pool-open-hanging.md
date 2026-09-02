# ZombieZen migration pool hangs on errors during Open

**Resolved in 0.21.1-beta.** What was taken and what was not is recorded at the bottom; the
research below is kept because the reasoning is still the reasoning.

**One correction to the premise first, because it changes which fixes are worth having.** Against
`zombiezen.com/go/sqlite@v1.4.2`, a failed *migration* does **not** hang. `Pool.open` returns it
(`sqlitemigration.go:266-273`), which sets `p.err`, closes `ready`, and lets `Take` report it —
`TestOpenRejectsForeignDatabase` is that path, and it passes in 0.01s. Only a failed **open** is
retried forever: `sqlitex.NewPool` failing is logged through `OnError` and followed by `continue`
(`sqlitemigration.go:249-252`).

`sqlitex.NewPool` in turn fails only when `sqlite.OpenConn` does, ten times eagerly
(`sqlitex/pool.go:106-113`); `PrepareConn` is stored there, not called. And `OpenConn` with
`OpenWAL` runs `PRAGMA journal_mode=wal` at open time (`sqlite.go:132-141`). That pragma is what
fails on a file that is not a database, and it is what blocks on one another process has locked.

So the reachable causes are narrow: a file that is not a SQLite database, a file this process
cannot open at all (permissions, a directory in the way, file descriptors exhausted), and a lock
held past the ten-second busy timeout `OpenConn` already sets around that pragma.

The methods below are the candidate mitigations, assessed after each one.

## 1. Enforce a Context Deadline on Pool.Get / Pool.Take

The most immediate way to prevent your application threads from hanging forever is to pass a timed context to Pool.Take(ctx). If the pool is stuck in its infinite retry-open loop, the call will exit with a timeout error once the context expires. [1, 2] 

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
// If the pool migration is failing/retrying, this will return an error after 5s
conn, err := dbPool.Take(ctx)
if err != nil {
    // Handle timeout or pool initialization failure gracefully
}
```

**Assessment: half taken.** Bounding the wait is right; a *deadline* is not. A fixed deadline puts
a guess on how long a large database may take to migrate — and a migration is exactly the case that
was never hanging, so the guess buys nothing and can kill honest work. It also reports
`context deadline exceeded`, which `DescribeOpenError` can only answer with its catch-all: worse
advice than the sentinel the failure actually has. What was taken is the cancellation, driven by
#2 rather than by a clock.

## 2. Capture and Panic on Initial Errors via OnError

The pool accepts a sqlitemigration.Options struct upon configuration. By leveraging the OnError callback hook, you can intercept the exact error causing the open failure and force a program exit or alert a monitoring system before the loop retries. [3] 

```go
opts := sqlitemigration.Options{
    Flags:    sqlite.OpenReadWrite | sqlite.OpenCreate,
    Migrations: myMigrations,
    OnError: func(err error) {
        // This triggers immediately when an open/migration attempt fails
        log.Fatalf("Critical SQLite pool initialization failure: %v", err)
    },
}

pool := sqlitemigration.NewPool(dbPath, opts)
```

**Assessment: taken, without the `log.Fatalf`.** Ending the process is wrong here: `Open` is
reached from `POST /checkbook/open`, so a mistyped path in the browser would kill the register out
from under the household, and it contradicts the rule that a database which will not open is served
as a page (RG-4, PL-4).

`OnError` is valuable for a different reason. It is the only place the retried error is visible at
all — otherwise it is discarded — and, because a migration failure never reaches it, **its first
call is proof that the pool is in the retry loop rather than doing slow but honest work**. That is
what makes bounding the wait possible without guessing a duration: record the error, cancel, and
report what was recorded. See `openFailure` in `internal/storage/storage.go`.

## 3. Check for Pre-Existing File and Database Locks

Infinite open retries are frequently triggered by transient errors like SQLITE_BUSY (database is locked). Ensure you are not deadlocking your own application during startup: [4, 5] 

* Set a Busy Timeout: Always include a _busy_timeout parameter in your URI connection string to instruct SQLite to wait for file locks to clear rather than failing immediately.

```go
dbPath := "file:app.db?_busy_timeout=5000"
```

* Single-Process Access: Ensure another process (or a zombie container instance) isn't holding an exclusive write lock on the target file. [7]

**Assessment: not taken. The syntax is from another library, and the concern is already handled.**
`_busy_timeout` is a `mattn/go-sqlite3` parameter — citation [6] is that repository — and zombiezen
has none. It would also be actively harmful in this program: `Open` reaches the pool through
`sqlitemigration`, which leaves `Flags` zero, so sqlitex applies `OpenURI` and a `?` in a folder
name is already parsed as a parameter. That is the hazard `OpenReadOnly` and `ApplicationID` go out
of their way to avoid, and widening it would be a step backwards.

The underlying question is answered by the library: `sqlite.OpenConn` sets a **ten-second busy
timeout specifically around the WAL pragma** before running it (`sqlite.go:132-141`).

What this section is right about is the *scenario*. A lock held past those ten seconds is reachable
here — two copies of this program on one database are supported by design — and it is the case the
header check in `refuseBackup` cannot see, because the file is a perfectly good checkbook.
`TestOpenReportsALockedDatabaseRatherThanRetryingForever` pins it.

## 4. Explicitly Close the Pool to Break the Loop

If you detect a system fault elsewhere in your application, invoking pool.Close() will explicitly terminate the internal background migration goroutine and break the retry loop immediately. [1, 2]

**Assessment: already done, with one refinement.** `storage.open` has always called `pool.Close()`
on every failure path. The refinement is that a pool abandoned to the retry loop is now closed
**off the calling goroutine**. `Take` feeds the retry channel before it waits, so an abandoned pool
has already begun one more round of ten `OpenConn` calls, and `Close` waits for it: against a
locked database that turned one ten-second busy timeout into two. Measured 20.2s before, 10.1s
after. The caller has nothing left to do with the pool, and it shuts itself down either way — the
wait was the only thing being paid for.

---

## What was done

Two layers, because they answer different questions.

**`refuseBackup` reads the header before the pool is built**, and refuses a file that has bytes and
is not a SQLite database. That is the common case and the one with a real answer: the reader gets
`ErrNotCheckbook` and the page that names it, rather than SQLite's text through a catch-all. An
empty file still opens, because SQLite will initialize one.

**`openFailure` is the backstop for everything the header cannot see** — a good database that will
not open *now*. It is #1 and #2 combined: `OnError` records the first failure and cancels, `Get`
returns, and the recorded error is reported in place of the cancellation. No duration is guessed,
and a slow migration is never interrupted, because it holds `ready` closed and `Get` keeps waiting
on the caller's own context.

Substituting the recorded error is not cosmetic: `Take`'s `select` picks at random when both
`ready` and `ctx.Done()` are ready, so without it a genuine error could surface as
"context canceled".

Why it mattered more than a slow page: `POST /checkbook/open` calls `storage.Open` with the
request's context, which for a browser that stays connected has no bound, and it does so **holding
the control lock** — so a wait here blocked Close and Quit behind it for as long as the tab was
open.

Pinned by `TestOpenRefusesAFileThatIsNotADatabaseAtOnce`,
`TestOpenReportsAFileItCannotOpenRatherThanRetryingForever`, and
`TestOpenReportsALockedDatabaseRatherThanRetryingForever`.

[1] [https://pkg.go.dev](https://pkg.go.dev/zombiezen.com/go/sqlite/sqlitemigration)
[2] [https://pkg.go.dev](https://pkg.go.dev/zombiezen.com/go/sqlite/sqlitex)
[3] [https://pkg.go.dev](https://pkg.go.dev/zombiezen.com/go/sqlite/sqlitemigration)
[4] [https://github.com](https://github.com/zombiezen/go-sqlite/discussions)
[5] [https://github.com](https://github.com/zombiezen/go-sqlite/discussions/96)
[6] [https://github.com](https://github.com/mattn/go-sqlite3/issues/290)
[7] [https://github.com](https://github.com/open-webui/docs/blob/main/docs/troubleshooting/manual-database-migration.md)

