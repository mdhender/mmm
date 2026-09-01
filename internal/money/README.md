# Money

Package `money` provides exact money values for ledger entries.

Money is represented as an integer number of minor units plus an explicit currency code.
Arithmetic only succeeds for matching currencies, which keeps accidental cross-currency addition or comparison out of the ledger.

```go
usd, err := money.ParseDecimal("123.45", money.USD)
if err != nil {
	return err
}

fee := money.MustNewMinor(250, money.USD)
total, err := usd.Add(fee)
if err != nil {
	return err
}

fmt.Println(total.String()) // USD 125.95
```

## Currency Scale

The package ships with a small default scale registry:

- `USD`, `EUR`, and `GBP`: 2 decimal places.
- `JPY`: 0 decimal places.
- `KWD`: 3 decimal places.

Additional currencies or accounting units can be registered:

```go
err := money.RegisterCurrency("BTC", 8)
```

## JSON

Money values marshal to this shape:

```json
{"amount":"123.45","currency":"USD"}
```

The amount is a canonical decimal string.
Floating-point helpers are intentionally not part of the package because ledger arithmetic must be exact.

**JSON is for the wire, not for storage.** Do not persist a money value by marshalling it to JSON.
An amount stored as JSON cannot be summed, compared, or indexed by SQLite, and those are most of
what a register needs from an amount column. Store the `int64` from `Amount()` in an `INTEGER`
column alongside a currency, and reconstruct with `NewMinor`.

`internal/storage` has `BindMoney` and `ColumnMoney` for exactly this. Two traps they exist to
avoid:

- ZombieZen SQLite is deliberately not a `database/sql` driver, so there is no `Valuer`/`Scanner`
  hook. A money value cannot encode itself; every bind has to be explicit.
- `Money` has a `String` method, so passing one to `sqlitex.ExecOptions.Args` does not fail. Args
  falls back to `fmt.Sprint` for unknown kinds and writes the text `"USD 125.95"` into the column;
  reading it back as an integer then yields `0`. The schema's `typeof()` CHECK constraints turn
  that silent corruption into an error.
