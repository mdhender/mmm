# Local Web UI

I mean something slightly unusual:

```text
checkbook.exe
    |
    +-- SQLite
    |
    +-- Go templates / HTMX
    |
    +-- HTTP server bound to 127.0.0.1
              |
              +--> default browser
```

The Windows user double-clicks checkbook.exe. It starts an HTTP server on localhost and opens:

```text
http://127.0.0.1:xxxxx/
```

There is no server installation, Node installation, database server, browser extension, account, subscription, or cloud service.

On your development machines:

```text
go build ./cmd/checkbook
```

That's essentially the deployment architecture.

For this particular application, that is awfully attractive.
