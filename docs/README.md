# Documentation

`mmm` is a local-first household checkbook. Its documentation is organized by
[Diataxis](https://diataxis.fr): four kinds of document, each answering a different need, kept
apart so that none of them has to be three things at once.

| If you want to… | Read |
| --- | --- |
| understand what this is and whether it is for you | [Explanation](#explanation) |
| get something done | [How-to guides](#how-to-guides) |
| look up a detail while working | [Reference](#reference) |

**New here?** Read [About mmm](explanations/what-is-mmm.md) first, then
[How to create your first checkbook](how-to/create-your-first-checkbook.md).

**A caution before you plan around it:** this release *displays* a register and nothing more. It
cannot yet create accounts, enter transactions, import, export, reconcile, or back up for you.
Every document below says so where it matters, but it is easier to hear once, up front.

## How-to guides

Directions for a particular job. They assume you know what you are trying to do.

- [How to create your first checkbook](how-to/create-your-first-checkbook.md) — choose where your
  records live, create the file, and prove you can restore a copy of it
- [How to upgrade the application](how-to/upgrade-the-application.md) — move to a newer version
  without risking the records, and get back if it goes wrong
- [How to report a problem](explanations/how-to-report-problems.md) — what to collect, what never
  to attach, and where to send it

## Reference

Description of the machinery, for consulting rather than reading.

- [User manual](references/user-manual.md) — options, the database file, every register column and
  total, the addresses served, and the exact messages a failed startup prints. States the version
  it applies to.

## Explanation

Background and discussion, for when you are away from the program and thinking about it.

- [About mmm](explanations/what-is-mmm.md) — why a checkbook rather than a financial platform, why
  files rather than a service, why it does not connect to your bank and what that costs you, and
  who would be better served by something else

## Tutorials

None yet. A tutorial teaches through doing, and the doing — entering your first transactions,
reconciling your first statement — is not built. There will be one when there is.

## For people working on the program

These are not end-user documentation. They are design intent and internal rules, and they are
sometimes ahead of the code.

- [`MANIFESTO.md`](../MANIFESTO.md) — what is being built and why; settles questions of taste
- [`SPECIFICATION.md`](../SPECIFICATION.md) — the binding, numbered requirements
- [`CLAUDE.md`](../CLAUDE.md) — conventions and constraints for working in this repository

Design notes for individual slices, **not binding** — where one conflicts with the specification,
the specification wins:

- [architecture.md](architecture.md), [project-structure.md](project-structure.md)
- [sqlite-store.md](sqlite-store.md), [data-imports.md](data-imports.md),
  [reconciliation.md](reconciliation.md)
- [local-web-ui.md](local-web-ui.md), [htmx-web-ui.md](htmx-web-ui.md),
  [local-text-ui.md](local-text-ui.md)
- [inspiration.md](inspiration.md)
