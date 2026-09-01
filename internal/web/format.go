// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"strings"

	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/transaction"
)

// formatAmount renders m for a register column: the exact decimal the money
// package produces, with the integer part grouped in threes.
//
// The grouping is done on the text money already returned. Nothing here converts
// the amount to a float or recomputes it (SPECIFICATION.md CO-1); this function
// only inserts separators.
func formatAmount(m money.Money) string {
	return groupDigits(m.Decimal())
}

// groupDigits inserts a comma every three digits of the integer part of a
// decimal string such as "-4817.29".
func groupDigits(s string) string {
	sign := ""
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		sign, s = "-", rest
	}

	whole, frac, hasFrac := strings.Cut(s, ".")

	var b strings.Builder
	b.WriteString(sign)
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String()
}

// statusMark is the single character shown in the register's status column.
//
// The convention is the paper one: nothing while a transaction is only in the
// checkbook, a lowercase c once the bank has shown it, and a capital R once a
// reconciliation has recorded it. Cleared and reconciled are different facts and
// the column keeps them apart (SPECIFICATION.md RC-3).
func statusMark(s transaction.Status) string {
	switch s {
	case transaction.Cleared:
		return "c"
	case transaction.Reconciled:
		return "R"
	default:
		return ""
	}
}

// statusLabel is the spelled-out status, used as the column's tooltip so the
// mark is never the only explanation.
func statusLabel(s transaction.Status) string {
	switch s {
	case transaction.Cleared:
		return "Cleared"
	case transaction.Reconciled:
		return "Reconciled"
	case transaction.Uncleared:
		return "Uncleared"
	default:
		return string(s)
	}
}

// categoryLabel is what the category column shows.
//
// A transaction split among several categories names none of them: showing the
// first would suggest the whole amount went there. An uncategorized transaction
// says so rather than showing a blank cell that could be mistaken for a
// rendering fault.
func categoryLabel(e transaction.Entry) string {
	switch {
	case e.IsSplit():
		return "— Split —"
	case e.Category == "":
		return "Uncategorized"
	default:
		return e.Category
	}
}
