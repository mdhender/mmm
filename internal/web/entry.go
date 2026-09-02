// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/transaction"
)

// entryForm is what the reader typed, kept as text so a page that comes back
// with a problem comes back with the entry still in it. Nothing here is
// converted to money or to a date: those are parse results, and holding both
// would let them disagree.
type entryForm struct {
	Date        string
	CheckNumber string
	Payee       string
	Memo        string
	Category    string

	// Payment and Deposit are the two amount boxes of a paper register. Exactly
	// one is filled. Keeping them apart is what makes a missing minus sign
	// impossible rather than merely unlikely: there is no sign to miss.
	Payment string
	Deposit string
}

// blankEntryForm is the form as a fresh register shows it: today's date, and
// nothing else.
//
// The date is the server's local calendar date. It is not an instant and is
// never converted (SPECIFICATION.md ST-8) -- the household and the machine are
// in the same room, so "today" here is the household's today.
func blankEntryForm() entryForm {
	return entryForm{Date: time.Now().Format(transaction.DateLayout)}
}

// entry is a validated entry, ready to write. Category is the name the reader
// typed, empty for an uncategorized entry; it is not resolved to an id here
// because that needs the database and this parse does not.
type entry struct {
	New      transaction.New
	Category string
}

// parseEntryForm validates what was typed against acct.
//
// It returns the entry to write, the form to redisplay, and a problem to show
// above it. A non-empty problem means nothing should be written. The problem
// says what happened and what to do next, because a form that only says "invalid
// amount" is the same dead end an error page without a next step would be
// (SPECIFICATION.md RG-4).
//
// It is pure so the rules can be tested without an HTTP request or a database.
func parseEntryForm(v url.Values, acct account.Account) (entry, entryForm, string) {
	form := entryForm{
		Date:        strings.TrimSpace(v.Get("date")),
		CheckNumber: strings.TrimSpace(v.Get("check_number")),
		Payee:       strings.TrimSpace(v.Get("payee")),
		Memo:        strings.TrimSpace(v.Get("memo")),
		Category:    strings.TrimSpace(v.Get("category")),
		Payment:     strings.TrimSpace(v.Get("payment")),
		Deposit:     strings.TrimSpace(v.Get("deposit")),
	}

	if form.Date == "" {
		return entry{}, form, "Every entry needs a date. Type the date it happened as YYYY-MM-DD, then press Add again."
	}
	// The date is parsed here as well as in transaction.Create. Create's job is
	// to refuse bad data; this one's is to say something the reader can act on.
	if _, err := time.Parse(transaction.DateLayout, form.Date); err != nil {
		return entry{}, form, fmt.Sprintf(
			"%q is not a calendar date. Write the date as YYYY-MM-DD, for example 2026-09-01, then press Add again.", form.Date)
	}

	if form.Payee == "" {
		return entry{}, form, "Every entry needs a payee. Type who was paid, or who paid you, then press Add again."
	}

	switch {
	case form.Payment != "" && form.Deposit != "":
		return entry{}, form, "An entry is a payment or a deposit, not both. Clear whichever box does not apply, then press Add again."
	case form.Payment == "" && form.Deposit == "":
		return entry{}, form, "Every entry needs an amount. Type it under Payment if money left the account, or under Deposit if money arrived, then press Add again."
	}

	text, box := form.Deposit, "Deposit"
	if form.Payment != "" {
		text, box = form.Payment, "Payment"
	}

	amount, problem := parseEntryAmount(text, box, acct.Currency)
	if problem != "" {
		return entry{}, form, problem
	}

	// A payment leaves the account, so it is stored negative. The negation goes
	// through the money package rather than through the typed text, so nothing
	// here does arithmetic on an amount (CO-1).
	if box == "Payment" {
		zero, err := money.Zero(acct.Currency)
		if err != nil {
			return entry{}, form, fmt.Sprintf(
				"This account is denominated in %s, which this build does not recognize. Report this; it is a fault in the program, not in what you typed.", acct.Currency)
		}
		negated, err := zero.Subtract(amount)
		if err != nil {
			return entry{}, form, fmt.Sprintf(
				"%s could not be recorded as a payment. Report this; it is a fault in the program, not in what you typed.", text)
		}
		amount = negated
	}

	return entry{
		New: transaction.New{
			Date:        form.Date,
			Payee:       form.Payee,
			Memo:        form.Memo,
			Amount:      amount,
			CheckNumber: form.CheckNumber,
			// Status is left empty: Create defaults it to uncleared, which is
			// what a freshly written entry is. Marking it cleared is a separate
			// act, because the bank showing it is a separate fact (RC-3).
		},
		Category: form.Category,
	}, form, ""
}

// parseEntryAmount reads one of the two amount boxes as money in cur.
//
// The amount is unsigned: the box it was typed in already says which way the
// money went, so a sign is refused rather than interpreted. Scale is the money
// package's rule, not one restated here, so a third decimal place is refused in
// USD and accepted in KWD without this function knowing either.
func parseEntryAmount(text, box string, cur money.Currency) (money.Money, string) {
	if strings.HasPrefix(text, "-") || strings.HasPrefix(text, "+") {
		return money.Money{}, fmt.Sprintf(
			"Type the amount under %s without a sign; the box already says which way the money went. Remove the sign, then press Add again.", box)
	}

	amount, err := money.ParseDecimal(text, cur)
	if err != nil {
		if scale, ok := money.Scale(cur); ok {
			if _, frac, hasFrac := strings.Cut(text, "."); hasFrac && len(frac) > scale {
				return money.Money{}, fmt.Sprintf(
					"%s is recorded to %d decimal places, so %q is more precise than this account can hold. Round it, then press Add again.",
					cur, scale, text)
			}
		}
		return money.Money{}, fmt.Sprintf(
			"%q is not an amount. Type digits and at most one decimal point, such as 84.17, then press Add again.", text)
	}

	if amount.IsZero() {
		return money.Money{}, fmt.Sprintf(
			"An entry for zero would not change the balance. Type the amount under %s, then press Add again.", box)
	}

	return amount, ""
}

// handleCreateTransaction writes one entry and sends the reader back to the
// register.
//
// It answers with a redirect rather than a page (RG-1's register is what should
// be on screen afterwards), and the redirect is what keeps a reload from
// entering the transaction twice. A silently duplicated transaction is exactly
// the kind of quiet loss that costs a register its credibility (SC-4).
func (s *Server) handleCreateTransaction(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts, so the entry was not written.",
			"Check that the file exists and is not open in another program, then go back and enter it again. Nothing was recorded.")
		return
	}

	acct, ok := s.accountFor(w, r, accounts)
	if !ok {
		return
	}

	if acct.IsClosed() {
		// A closed account is a finished record. Reopening it is a decision, not
		// a side effect of typing into a form that happened to still be on screen.
		s.fail(w, r, http.StatusConflict, accounts,
			"That account is closed",
			acct.Name+" was closed on "+acct.ClosedOn+", so no entry was written to it.",
			"Choose an open account from the list on the left. If this entry belongs to "+acct.Name+", the account has to be reopened first.")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, accounts,
			"That entry could not be read",
			"The browser sent a form this program could not decode, so nothing was written.",
			"Go back to the register and enter it again.")
		return
	}

	// PostForm, not Form: only what was typed into the form counts, so a value
	// in the query string cannot stand in for a field.
	ent, form, problem := parseEntryForm(r.PostForm, acct)
	if problem != "" {
		// 422: the request was well formed and understood, and refused on its
		// contents. The page comes back with the entry still in it.
		s.renderRegister(w, r, http.StatusUnprocessableEntity, accounts, acct, form, problem)
		return
	}

	if ent.Category != "" {
		cat, err := category.Ensure(r.Context(), s.store, ent.Category)
		if err != nil {
			s.log.Error("ensure category", "name", ent.Category, "err", err)
			s.fail(w, r, http.StatusInternalServerError, accounts,
				"That category could not be recorded",
				"The database reported an error while looking up the category "+ent.Category+", so the entry was not written.",
				"Go back to the register and enter it again. Nothing was recorded.")
			return
		}
		// One split for the whole amount, so the totals check in Create is
		// trivially satisfied and the row has a category to show.
		ent.New.Splits = []transaction.Split{{CategoryID: cat.ID, Amount: ent.New.Amount}}
	}

	txn, err := transaction.Create(r.Context(), s.store, acct, ent.New)
	if err != nil {
		// The form already refused everything below, so reaching one of these
		// means the two disagree. Say so on the form rather than as a 500: the
		// reader can still fix it, and the entry is still on screen.
		if errors.Is(err, transaction.ErrInvalidDate) ||
			errors.Is(err, transaction.ErrInvalidStatus) ||
			errors.Is(err, transaction.ErrSplitTotal) ||
			errors.Is(err, money.ErrCurrencyMismatch) {
			s.log.Error("create transaction refused after the form accepted it", "account", acct.Name, "err", err)
			s.renderRegister(w, r, http.StatusUnprocessableEntity, accounts, acct, form,
				"That entry was refused when it was written, and nothing was recorded. Check the date and the amount, then press Add again.")
			return
		}
		s.log.Error("create transaction", "account", acct.Name, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"That entry could not be written",
			"The database reported an error while writing to "+acct.Name+". The entry and its category were written together or not at all, so the register is not half-changed.",
			"Reload the register to see whether it arrived. If it did not, enter it again; if the error repeats, restore your most recent backup.")
		return
	}

	// See Other, so the reload that follows is a GET of the register and not a
	// second entry. The fragment is the row's own id, which the register
	// template already puts on every row.
	http.Redirect(w, r, fmt.Sprintf("/accounts/%d#txn-%d", acct.ID, txn.ID), http.StatusSeeOther)
}

// accountFor reads the account named by the request path, writing the error page
// itself if there is not one. The register and the entry handler answer a bad id
// identically, and answering it in two places is how they would drift apart.
func (s *Server) accountFor(w http.ResponseWriter, r *http.Request, accounts []account.Account) (account.Account, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, accounts,
			"That is not an account address",
			"The address "+r.URL.Path+" does not name an account. An account address looks like /accounts/1.",
			"Choose an account from the list on the left.")
		return account.Account{}, false
	}

	acct, err := account.Get(r.Context(), s.store, id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			// Ids are never reused (ST-9), so a missing one means the account was
			// deleted, not that it turned into a different account. Say so.
			s.fail(w, r, http.StatusNotFound, accounts,
				"No such account",
				"There is no account numbered "+strconv.FormatInt(id, 10)+" in this database. A bookmark or an open tab may be pointing at an account that was removed.",
				"Choose an account from the list on the left.")
			return account.Account{}, false
		}
		s.log.Error("get account", "id", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"That account could not be read",
			"The database reported an error while reading account "+strconv.FormatInt(id, 10)+".",
			"Reload this page. If it keeps failing, open the database file with another SQLite tool to check it, and restore your most recent backup if it is damaged.")
		return account.Account{}, false
	}

	return acct, true
}
