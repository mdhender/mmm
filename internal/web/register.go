// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"net/http"
	"strconv"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// registerPage is everything the register template needs, already formatted.
// Amounts are strings here on purpose: the template's job is to place them, not
// to decide how money looks.
type registerPage struct {
	layout

	Account  account.Account
	Currency string
	Rows     []registerRow

	// Ending is the balance after every row; Cleared is the balance the bank has
	// seen. Both are shown, along with the difference between them, because
	// reconciliation depends on the gap being visible rather than resolved
	// (SPECIFICATION.md RC-1, RC-2).
	Ending         string
	EndingNegative bool
	Cleared        string
	Uncleared      string
	UnclearedCount int

	// Form is the entry form under the table, holding whatever was last typed
	// so a refused entry comes back with the reader's work still in it.
	Form entryForm

	// FormError is shown above the form. It is empty on a register nobody has
	// just tried to write to.
	FormError string

	// RowNotice reports something that happened to a row rather than to the
	// entry form: a change refused because another tab got there first. It is
	// named apart from the frame's Notice because the two are different things
	// and an embedded field of the same name would quietly hide one of them.
	RowNotice string

	// OOB marks a fragment response rather than a whole page, so the totals
	// carry hx-swap-oob and are replaced in place. It is false on every full
	// render.
	OOB bool

	// Categories are the names already in use, offered to the category box as
	// suggestions. The box still takes anything typed: this is what stops a
	// slip creating "Grocerys" beside "Groceries", not a restriction on what a
	// category may be called.
	Categories []string
}

// registerRow is one line of the register.
type registerRow struct {
	ID            int64
	AccountID     int64
	Date          string
	CheckNumber   string
	Payee         string
	Category      string
	Uncategorized bool
	Memo          string

	Amount          string
	AmountNegative  bool
	Balance         string
	BalanceNegative bool

	StatusMark  string
	StatusLabel string

	// Token is the row's updated_at, sent back with a change so a write from a
	// tab holding a stale value is refused rather than applied (CO-3).
	Token string

	// Reconciled rows carry no control. A finished reconciliation is a fact, and
	// the register does not rewrite it (RC-3).
	Reconciled bool

	// ReadOnly carries the frame's own flag down to the row, because a row is
	// also rendered on its own -- as the answer to marking one cleared -- where
	// there is no layout to read it from.
	ReadOnly bool

	// NextStatus is what the control sets, and ActionLabel names it. The mark is
	// a toggle, so both flip with the row's current status.
	NextStatus  string
	ActionLabel string
}

// handleRoot sends the reader to an account, or explains that there are none.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, err := account.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+cb.path+" reported an error while listing accounts.",
			"Check that the file exists and is not open in another program, then reload this page. Your records are not changed by reading them.")
		return
	}

	if len(accounts) == 0 {
		s.render(w, r, http.StatusOK, "empty.gohtml",
			struct{ layout }{s.pageLayout(r, cb, "No accounts yet", nil, 0)})
		return
	}

	// The register is the primary screen (RG-1), so the root goes straight to
	// one rather than to a menu. Each account keeps its own URL, which is what
	// makes a bookmark or a second tab mean something.
	http.Redirect(w, r, "/accounts/"+strconv.FormatInt(accounts[0].ID, 10), http.StatusFound)
}

// handleNotFound answers an address the program does not serve.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	// The account list is fetched so the page still offers somewhere to go. A
	// failure to read it is not worth a second error page here.
	accounts, err := account.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		accounts = nil
	}
	s.fail(w, r, cb, http.StatusNotFound, accounts,
		"There is no page at that address",
		"Nothing in the checkbook answers to "+r.URL.Path+".",
		"Choose an account from the list on the left, or go back to the register.")
}

// handleRegister renders one account's register.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, err := account.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+cb.path+" reported an error while listing accounts.",
			"Check that the file exists and is not open in another program, then reload this page. Your records are not changed by reading them.")
		return
	}

	acct, ok := s.accountFor(w, r, cb, accounts)
	if !ok {
		return
	}

	s.renderRegister(w, r, cb, http.StatusOK, accounts, acct, blankEntryForm(), "")
}

// renderRegister reads acct's register and writes the page, carrying form and
// formError into the entry form beneath the table.
//
// Both the register handler and the entry handler end here, so a refused entry
// comes back on the same page it was typed on, with the same balances, rather
// than on a page of its own that would have to be kept in step with this one.
func (s *Server) renderRegister(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, accounts []account.Account, acct account.Account, form entryForm, formError string) {
	reg, err := transaction.LoadRegister(r.Context(), cb.store, acct)
	if err != nil {
		s.log.Error("load register", "account", acct.Name, "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"The register could not be read",
			"The transactions for "+acct.Name+" could not be listed, so no balance is shown. Showing a partial register would be worse than showing none.",
			"Reload this page. If it keeps failing, restore your most recent backup.")
		return
	}

	// The suggestions are a convenience, so a failure to read them is not worth
	// refusing a register the reader can otherwise use. It is logged and the box
	// simply offers nothing; anything genuinely wrong with the database has
	// already failed LoadRegister above.
	categories, err := category.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list categories", "err", err)
	}
	names := make([]string, 0, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
	}

	page := buildRegisterPage(s.pageLayout(r, cb, acct.Name, accounts, acct.ID), reg)
	page.Form = form
	page.FormError = formError
	page.Categories = names

	s.render(w, r, status, "register.gohtml", page)
}

// buildRegisterPage formats a register for display. It is separate from the
// handler so the formatting can be tested without an HTTP request.
func buildRegisterPage(l layout, reg transaction.Register) registerPage {
	page := registerPage{
		layout:         l,
		Account:        reg.Account,
		Currency:       string(reg.Account.Currency),
		Rows:           make([]registerRow, 0, len(reg.Entries)),
		Ending:         formatAmount(reg.Ending),
		EndingNegative: reg.Ending.IsNegative(),
		Cleared:        formatAmount(reg.Cleared),
		UnclearedCount: reg.UnclearedCount,
	}

	// The uncleared total is stated rather than left for the reader to subtract.
	// It is only meaningful when both balances are in the account's currency,
	// which LoadRegister has already guaranteed.
	if uncleared, err := reg.Ending.Subtract(reg.Cleared); err == nil {
		page.Uncleared = formatAmount(uncleared)
	}

	for _, e := range reg.Entries {
		page.Rows = append(page.Rows, buildRegisterRow(reg.Account, e, l.ReadOnly))
	}
	return page
}

// buildRegisterRow formats one entry. It is its own function because a single
// row is also rendered on its own, as the answer to marking one cleared.
func buildRegisterRow(acct account.Account, e transaction.Entry, readOnly bool) registerRow {
	row := registerRow{
		ID:              e.ID,
		AccountID:       acct.ID,
		Date:            e.Date,
		CheckNumber:     e.CheckNumber,
		Payee:           e.Payee,
		Category:        categoryLabel(e),
		Uncategorized:   !e.IsSplit() && e.Category == "",
		Memo:            e.Memo,
		Amount:          formatAmount(e.Amount),
		AmountNegative:  e.Amount.IsNegative(),
		Balance:         formatAmount(e.Balance),
		BalanceNegative: e.Balance.IsNegative(),
		StatusMark:      statusMark(e.Status),
		StatusLabel:     statusLabel(e.Status),
		Token:           storage.FormatTime(e.UpdatedAt),
		Reconciled:      e.Status == transaction.Reconciled,
		ReadOnly:        readOnly,
	}

	// The control offers the other state, so the label says what pressing it
	// does rather than what the row already is.
	if e.Status.HasCleared() {
		row.NextStatus, row.ActionLabel = string(transaction.Uncleared), "Mark not cleared"
	} else {
		row.NextStatus, row.ActionLabel = string(transaction.Cleared), "Mark cleared"
	}
	return row
}
