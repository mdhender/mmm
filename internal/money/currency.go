// Copyright (c) 2026 Michael D Henderson.

// Package money implements exact money values for ledger entries.
package money

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/mdhender/mmm/internal/cerrs"
)

// Currency identifies a currency or accounting unit.
type Currency string

const (
	USD Currency = "USD"
	EUR Currency = "EUR"
	GBP Currency = "GBP"
	JPY Currency = "JPY"
	KWD Currency = "KWD"
)

const (
	// ErrCurrencyMismatch is returned when arithmetic combines unlike currencies.
	ErrCurrencyMismatch = cerrs.Error("currency mismatch")

	// ErrInvalidCurrency is returned when a currency code is unknown or malformed.
	ErrInvalidCurrency = cerrs.Error("invalid currency")

	// ErrInvalidAmount is returned when a decimal amount cannot be represented exactly.
	ErrInvalidAmount = cerrs.Error("invalid amount")
)

var registry = struct {
	sync.RWMutex
	scales map[Currency]int
}{
	scales: map[Currency]int{
		USD: 2,
		EUR: 2,
		GBP: 2,
		JPY: 0,
		KWD: 3,
	},
}

// Money stores an exact amount in minor units and its currency.
type Money struct {
	amount   int64
	currency Currency
}

// RegisterCurrency records the decimal scale for a currency or accounting unit.
func RegisterCurrency(currency Currency, scale int) error {
	if err := validateCurrencyCode(currency); err != nil {
		return err
	}
	if scale < 0 || scale > 18 {
		return fmt.Errorf("%w: scale must be between 0 and 18", ErrInvalidCurrency)
	}
	registry.Lock()
	defer registry.Unlock()
	registry.scales[currency] = scale
	return nil
}

// Scale returns the number of decimal places used by a currency.
func Scale(currency Currency) (int, bool) {
	registry.RLock()
	defer registry.RUnlock()
	scale, ok := registry.scales[currency]
	return scale, ok
}

// Currencies returns every currency this build knows, in code order.
//
// A form that offers a currency has to offer the ones the registry actually
// holds. Listing them here rather than in the interface is what stops a select
// box drifting from the registry the moment RegisterCurrency adds to it.
func Currencies() []Currency {
	registry.RLock()
	defer registry.RUnlock()

	out := make([]Currency, 0, len(registry.scales))
	for currency := range registry.scales {
		out = append(out, currency)
	}
	slices.Sort(out)
	return out
}

// NewMinor returns a Money value from an exact minor-unit amount.
func NewMinor(amount int64, currency Currency) (Money, error) {
	if _, ok := Scale(currency); !ok {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{amount: amount, currency: currency}, nil
}

// MustNewMinor returns a Money value or panics if the currency is invalid.
func MustNewMinor(amount int64, currency Currency) Money {
	m, err := NewMinor(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

// ParseDecimal parses an exact major-unit decimal amount.
func ParseDecimal(amount string, currency Currency) (Money, error) {
	scale, ok := Scale(currency)
	if !ok {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	minor, err := parseDecimalMinor(amount, scale)
	if err != nil {
		return Money{}, err
	}
	return Money{amount: minor, currency: currency}, nil
}

// Amount returns the amount in minor units.
func (m Money) Amount() int64 {
	return m.amount
}

// Currency returns the money value's currency.
func (m Money) Currency() Currency {
	return m.currency
}

// Decimal returns the canonical major-unit decimal representation.
func (m Money) Decimal() string {
	scale, ok := Scale(m.currency)
	if !ok {
		scale = 0
	}
	return formatDecimalMinor(m.amount, scale)
}

// String returns a stable human-readable representation.
// String renders the value for humans, as in "USD 123.45".
//
// Beware that having this method makes a Money survive fmt.Sprint, so passing
// one to a reflection-based database binder silently stores this text instead of
// failing. Bind Amount instead; see internal/storage.BindMoney.
func (m Money) String() string {
	return string(m.currency) + " " + m.Decimal()
}

// MarshalJSON encodes money using the API representation.
//
// This is for wire and export formats. Do NOT use it to store an amount in a
// database column: an amount held as JSON cannot be summed, compared, or indexed
// by SQLite, which is nearly everything the register asks of it. Persist the
// integer from Amount together with a currency, and read it back with NewMinor.
// See internal/storage for the binding helpers.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Amount   string   `json:"amount"`
		Currency Currency `json:"currency"`
	}{
		Amount:   m.Decimal(),
		Currency: m.currency,
	})
}

// UnmarshalJSON decodes money from the API representation.
func (m *Money) UnmarshalJSON(data []byte) error {
	var payload struct {
		Amount   string   `json:"amount"`
		Currency Currency `json:"currency"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	parsed, err := ParseDecimal(payload.Amount, payload.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func validateCurrencyCode(currency Currency) error {
	code := string(currency)
	if len(code) < 3 || len(code) > 16 {
		return fmt.Errorf("%w: currency code length must be between 3 and 16", ErrInvalidCurrency)
	}
	for _, r := range code {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return fmt.Errorf("%w: currency code must use uppercase letters and digits", ErrInvalidCurrency)
		}
	}
	return nil
}

func parseDecimalMinor(amount string, scale int) (int64, error) {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return 0, fmt.Errorf("%w: empty amount", ErrInvalidAmount)
	}
	sign := ""
	if amount[0] == '-' || amount[0] == '+' {
		if amount[0] == '-' {
			sign = "-"
		}
		amount = amount[1:]
	}
	whole, frac, ok := strings.Cut(amount, ".")
	if whole == "" {
		whole = "0"
	}
	if !ok {
		frac = ""
	}
	if !allDigits(whole) || !allDigits(frac) {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	if len(frac) > scale {
		return 0, fmt.Errorf("%w: %q has more than %d decimal places", ErrInvalidAmount, amount, scale)
	}
	frac += strings.Repeat("0", scale-len(frac))
	text := sign + whole + frac
	if text == "" || text == "-" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	minor, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	return minor, nil
}

func formatDecimalMinor(amount int64, scale int) string {
	sign := ""
	if amount < 0 {
		sign = "-"
		amount *= -1
	}
	digits := strconv.FormatInt(amount, 10)
	if scale == 0 {
		return sign + digits
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	point := len(digits) - scale
	return sign + digits[:point] + "." + digits[point:]
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
