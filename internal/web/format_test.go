// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"testing"

	"github.com/mdhender/mmm/internal/money"
)

// TestFormatAmount checks the separators only. The digits come from the money
// package and are never recomputed here (SPECIFICATION.md CO-1).
func TestFormatAmount(t *testing.T) {
	tests := []struct {
		minor    int64
		currency money.Currency
		want     string
	}{
		{0, money.USD, "0.00"},
		{4321, money.USD, "43.21"},
		{481729, money.USD, "4,817.29"},
		{-481729, money.USD, "-4,817.29"},
		{100000000, money.USD, "1,000,000.00"},
		{-8417, money.USD, "-84.17"},
		{99999, money.USD, "999.99"},
		// A currency with no minor units still groups its integer part.
		{1234567, money.JPY, "1,234,567"},
		// And one with three keeps all three.
		{1234567, money.KWD, "1,234.567"},
	}
	for _, tt := range tests {
		m, err := money.NewMinor(tt.minor, tt.currency)
		if err != nil {
			t.Fatalf("NewMinor(%d, %s): %v", tt.minor, tt.currency, err)
		}
		if got := formatAmount(m); got != tt.want {
			t.Errorf("formatAmount(%d %s) = %q, want %q", tt.minor, tt.currency, got, tt.want)
		}
	}
}
