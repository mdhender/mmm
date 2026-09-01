// Copyright (c) 2026 Michael D Henderson.

package money

// Zero returns a zero amount for the currency.
func Zero(currency Currency) (Money, error) {
	return NewMinor(0, currency)
}

// Abs returns the absolute value of the amount.
func (m Money) Abs() Money {
	if m.amount < 0 {
		return Money{amount: -m.amount, currency: m.currency}
	}
	return m
}

// Add returns the sum of same-currency money values.
func (m Money) Add(values ...Money) (Money, error) {
	amount := m.amount
	for _, value := range values {
		if err := m.requireSameCurrency(value); err != nil {
			return Money{}, err
		}
		amount += value.amount
	}
	return Money{amount: amount, currency: m.currency}, nil
}

// Subtract returns the difference of same-currency money values.
func (m Money) Subtract(values ...Money) (Money, error) {
	amount := m.amount
	for _, value := range values {
		if err := m.requireSameCurrency(value); err != nil {
			return Money{}, err
		}
		amount -= value.amount
	}
	return Money{amount: amount, currency: m.currency}, nil
}

// Compare returns -1, 0, or 1 for same-currency money values.
func (m Money) Compare(other Money) (int, error) {
	if err := m.requireSameCurrency(other); err != nil {
		return 0, err
	}
	switch {
	case m.amount < other.amount:
		return -1, nil
	case m.amount > other.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

// Equals reports whether two same-currency money values have the same amount.
func (m Money) Equals(other Money) (bool, error) {
	if err := m.requireSameCurrency(other); err != nil {
		return false, err
	}
	return m.amount == other.amount, nil
}

// IsGreaterThan reports whether m is greater than other.
func (m Money) IsGreaterThan(other Money) (bool, error) {
	cmp, err := m.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// IsLessThan reports whether m is less than other.
func (m Money) IsLessThan(other Money) (bool, error) {
	cmp, err := m.Compare(other)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// IsNegative reports whether the amount is less than zero.
func (m Money) IsNegative() bool {
	return m.amount < 0
}

// IsPositive reports whether the amount is greater than zero.
func (m Money) IsPositive() bool {
	return m.amount > 0
}

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool {
	return m.amount == 0
}

func (m Money) requireSameCurrency(other Money) error {
	if m.currency != other.currency {
		return ErrCurrencyMismatch
	}
	return nil
}
