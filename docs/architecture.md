# Architecture

## My preferred architecture

I'd start here:

```text
                      ┌──────────────┐
                      │   Browser    │
                      │ HTML + HTMX  │
                      └──────┬───────┘
                             │ localhost
                      ┌──────▼───────┐
                      │    net/http  │
                      │     Go       │
                      └──────┬───────┘
                             │
                ┌────────────┴────────────┐
                │                         │
         ┌──────▼──────┐           ┌──────▼──────┐
         │ domain      │           │ import/     │
         │ model       │           │ export      │
         └──────┬──────┘           └─────────────┘
                │
         ┌──────▼──────┐
         │   SQLite    │
         │ checkbook.db│
         └─────────────┘
```

**Go + ZombieZen SQLite + net/http + templates + HTMX.**

* No framework initially.

* No JavaScript build.

* No Wails.

* No Ember.

* No installer.

And crucially, **no architecture decision prevents Wails or Ember later**. The Go domain code remains unchanged.

I think there is a charming project hiding here: not "write a personal-finance application," but rather write the checkbook program Quicken stopped being twenty years ago. For v1, I'd define the product almost brutally narrowly: accounts, register, splits/categories, reconciliation, QIF/OFX/CSV import, search, and backups. Everything else has to justify its existence.
