// Copyright (c) 2026 Michael D Henderson.

package money_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/mdhender/mmm/internal/money"
)

func TestParseDecimal(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		currency money.Currency
		minor    int64
		decimal  string
	}{
		{name: "USD cents", input: "43.21", currency: money.USD, minor: 4321, decimal: "43.21"},
		{name: "negative USD", input: "-543.21", currency: money.USD, minor: -54321, decimal: "-543.21"},
		{name: "JPY no minor units", input: "987", currency: money.JPY, minor: 987, decimal: "987"},
		{name: "KWD three decimals", input: "12.345", currency: money.KWD, minor: 12345, decimal: "12.345"},
		{name: "pads decimals", input: "12.3", currency: money.USD, minor: 1230, decimal: "12.30"},
		{name: "leading decimal point", input: ".99", currency: money.USD, minor: 99, decimal: "0.99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.ParseDecimal(tt.input, tt.currency)
			if err != nil {
				t.Fatal(err)
			}
			if m.Amount() != tt.minor {
				t.Fatalf("Amount() = %d; want %d", m.Amount(), tt.minor)
			}
			if m.Currency() != tt.currency {
				t.Fatalf("Currency() = %s; want %s", m.Currency(), tt.currency)
			}
			if m.Decimal() != tt.decimal {
				t.Fatalf("Decimal() = %s; want %s", m.Decimal(), tt.decimal)
			}
		})
	}
}

func TestParseDecimalRejectsInexactScale(t *testing.T) {
	_, err := money.ParseDecimal("1.234", money.USD)
	if !errors.Is(err, money.ErrInvalidAmount) {
		t.Fatalf("ParseDecimal error = %v; want ErrInvalidAmount", err)
	}
}

func TestNewMinorRejectsUnknownCurrency(t *testing.T) {
	_, err := money.NewMinor(100, "XYZ")
	if !errors.Is(err, money.ErrInvalidCurrency) {
		t.Fatalf("NewMinor error = %v; want ErrInvalidCurrency", err)
	}
}

func TestRegisterCurrency(t *testing.T) {
	const btc money.Currency = "BTC"
	if err := money.RegisterCurrency(btc, 8); err != nil {
		t.Fatal(err)
	}
	m, err := money.ParseDecimal("1.00000001", btc)
	if err != nil {
		t.Fatal(err)
	}
	if m.Amount() != 100000001 {
		t.Fatalf("Amount() = %d; want 100000001", m.Amount())
	}
}

// TestCurrenciesListsWhatTheBuildKnows: a form offering a currency has to offer
// the ones the registry holds, including one registered at run time.
func TestCurrenciesListsWhatTheBuildKnows(t *testing.T) {
	const xts money.Currency = "XTS"
	if err := money.RegisterCurrency(xts, 2); err != nil {
		t.Fatal(err)
	}

	got := money.Currencies()
	if !slices.IsSorted(got) {
		t.Errorf("Currencies() = %v; want sorted", got)
	}
	for _, want := range []money.Currency{money.USD, money.EUR, money.GBP, money.JPY, money.KWD, xts} {
		if !slices.Contains(got, want) {
			t.Errorf("Currencies() = %v; missing %s", got, want)
		}
	}
	for _, c := range got {
		if _, ok := money.Scale(c); !ok {
			t.Errorf("Currencies() offers %s, which has no scale", c)
		}
	}
}

func TestAddRequiresSameCurrency(t *testing.T) {
	usd := money.MustNewMinor(100, money.USD)
	eur := money.MustNewMinor(100, money.EUR)
	_, err := usd.Add(eur)
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Add error = %v; want ErrCurrencyMismatch", err)
	}
}

func TestArithmetic(t *testing.T) {
	a := money.MustNewMinor(1000, money.USD)
	b := money.MustNewMinor(250, money.USD)
	sum, err := a.Add(b)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Decimal() != "12.50" {
		t.Fatalf("sum = %s; want 12.50", sum.Decimal())
	}
	diff, err := a.Subtract(b)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Decimal() != "7.50" {
		t.Fatalf("diff = %s; want 7.50", diff.Decimal())
	}
}

func TestJSON(t *testing.T) {
	m := money.MustNewMinor(12345, money.USD)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"amount":"123.45","currency":"USD"}`
	if string(data) != want {
		t.Fatalf("MarshalJSON = %s; want %s", data, want)
	}
	var got money.Money
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != m {
		t.Fatalf("UnmarshalJSON = %#v; want %#v", got, m)
	}
}

func TestString(t *testing.T) {
	m := money.MustNewMinor(-654321, money.USD)
	if m.String() != "USD -6543.21" {
		t.Fatalf("String() = %s; want USD -6543.21", m.String())
	}
}
