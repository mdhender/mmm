# HTMX Web UI

But consider what the application actually does:

```text
Accounts

Checking
Savings
Visa
Cash

--------------------------------------------------------------
Date       Payee                 Category        Amount  ✓
08/27/26   Riba Smith            Groceries      -84.17  *
08/28/26   Banco General         Transfer     +1000.00  *
08/29/26   Felipe Motta          Wine           -36.42
--------------------------------------------------------------
                                      Balance  4,817.29
```

Most operations are:

* add transaction
* edit transaction
* delete transaction
* split transaction
* mark cleared
* reconcile
* search/filter
* change accounts
* display totals

HTMX is nearly ideal for that.

You could have the entire application be:

```text
Go
net/http
html/template
HTMX
SQLite
```

and perhaps 100 lines of CSS.

* No REST API is necessary.

* No JSON is necessary.

* No client-side data model is necessary.

The register itself can be an HTML table whose rows get replaced by HTMX.

That is very much in the spirit of the software manifesto.
