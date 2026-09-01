# How to report a problem

This guide shows you how to report something that went wrong, in a form that can be acted on,
without exposing your household's finances.

`mmm` has no telemetry, no crash reporting, and no analytics — deliberately, because the simplest
way to avoid mishandling financial records is never to collect them. The consequence is that
nothing reaches the project on its own. If you do not report a problem, nobody knows it happened.

## 1. If your records might be affected, protect them first

Do this before anything else if you see wrong balances, transactions that are missing or
duplicated, or an error while the program was writing.

1. Stop the program with **Ctrl+C**.
2. Wait for the `-wal` and `-shm` files to disappear from the database folder.
3. Copy the database somewhere safe, under a name you will recognize later:

   ```sh
   cp checkbook.db "checkbook-problem-$(date +%Y%m%d-%H%M%S).db"
   ```

4. Leave that copy alone. It is both your fallback and the evidence.

Then, if you have an older backup you trust, work from that one until the problem is understood.

## 2. Try to reproduce it in the sample data

```sh
go run ./cmd/checkbook -demo
```

`-demo` serves a fixed sample household from memory and touches no files. If the problem happens
there too, you can describe every step of it without revealing a single real transaction — which
makes for the most useful kind of report, and the safest.

If it only happens with your own database, that is worth knowing too. Say so; do not go hunting
for a way to make the sample fail.

## 3. Collect the facts

| Fact | How to get it |
| --- | --- |
| Version | `go run ./cmd/checkbook -version` |
| Operating system and version | About This Mac, or Windows **Settings → System → About** |
| Browser and version | Only if the problem is on the register page |
| Go toolchain | `go version` |
| The exact command you ran | Copy it from your terminal, with the options |
| Terminal output | Everything the program printed, not only the last line |

The program writes its errors to the terminal it was started from, not to a log file. If you have
already closed that window, run it again and reproduce the problem before you write the report.

## 4. Describe the shape of your data, never the data

**Do not attach your database, and do not paste rows out of it.** It is your household's complete
financial history; a screenshot of the register is the same thing in a different format.

Describe the shape instead. These are all specific enough to act on and contain nothing private:

- "an account with about 400 transactions, three of them split"
- "a transaction dated 2026-08-27 with two splits, one of them uncategorized"
- "a payee containing an ampersand"
- "an account whose opening balance is negative"

If a screenshot is the only way to show a display problem, crop it to the part that is wrong and
paint over the payees and amounts around it.

## 5. File it

Open an issue at **<https://github.com/mdhender/mmm/issues>**.

One problem per report. Put what went wrong in the title — "register shows the wrong running
balance after a split", not "bug" — and paste this at the top of the body:

```text
Version:     0.5.0-beta
OS:          macOS 15.6
Browser:     Safari 18.2      (only if the problem is on the page)
Go:          go1.26.4
Command:     go run ./cmd/checkbook -db ~/Documents/checkbook/checkbook.db

What I did:
What I expected:
What happened instead:
Reproduces with -demo:  yes / no

Terminal output:
```

If you think the problem exposes records beyond your own machine, say so in the first line of the
report. There is no separate security contact yet.

## What counts as worth reporting

All of these, without hesitating over whether they are big enough:

- a number that is wrong, by any amount
- an error message that does not say what to do next, or says something that does not help
- the program refusing to start, or starting on a database you did not mean
- anything that made you check a balance twice

A checkbook is worth using only as far as it is worth believing. A report that turns out to be
your own mistake costs a few minutes; a wrong balance nobody mentioned costs the register its
credibility.

## Related

- [User manual](../references/user-manual.md) — the messages the program prints and what they mean
- [How to upgrade the application](../how-to/upgrade-the-application.md) — including how to go back
- [About mmm](what-is-mmm.md) — why there is no telemetry to report for you
