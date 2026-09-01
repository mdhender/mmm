# Data Imports

Imports are more important than bank synchronization.

This would be one of my strongest design decisions.

Don't connect directly to banks initially.

Support files.

Ideally:

* **QIF** - Very important because you're escaping Quicken.

* **OFX/QFX** - Useful for financial institution downloads.

* **CSV** - Because everything eventually speaks CSV.

Then importing is explicit:

```text
File → Import Transactions
```

and the application shows:

```text
12 new transactions
 2 possible duplicates
 0 errors

[ Review ] [ Import ]
```

That is vastly simpler than maintaining bank APIs.

And it's probably sufficient for someone who basically wants a checkbook.
