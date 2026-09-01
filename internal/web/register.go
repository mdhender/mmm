// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mdhender/mmm/internal/account"
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
}

// registerRow is one line of the register.
type registerRow struct {
	ID            int64
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
}

// handleRoot sends the reader to an account, or explains that there are none.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts.",
			"Check that the file exists and is not open in another program, then reload this page. Your records are not changed by reading them.")
		return
	}

	if len(accounts) == 0 {
		s.render(w, r, http.StatusOK, "empty.gohtml", struct{ layout }{layout{
			Title:    "No accounts yet",
			Database: s.store.Path(),
			Version:  s.version,
		}})
		return
	}

	// The register is the primary screen (RG-1), so the root goes straight to
	// one rather than to a menu. Each account keeps its own URL, which is what
	// makes a bookmark or a second tab mean something.
	http.Redirect(w, r, "/accounts/"+strconv.FormatInt(accounts[0].ID, 10), http.StatusFound)
}

// handleNotFound answers an address the program does not serve.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	// The account list is fetched so the page still offers somewhere to go. A
	// failure to read it is not worth a second error page here.
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		accounts = nil
	}
	s.fail(w, r, http.StatusNotFound, accounts,
		"There is no page at that address",
		"Nothing in the checkbook answers to "+r.URL.Path+".",
		"Choose an account from the list on the left, or go back to the register.")
}

// handleRegister renders one account's register.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts.",
			"Check that the file exists and is not open in another program, then reload this page. Your records are not changed by reading them.")
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, accounts,
			"That is not an account address",
			"The address "+r.URL.Path+" does not name an account. An account address looks like /accounts/1.",
			"Choose an account from the list on the left.")
		return
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
			return
		}
		s.log.Error("get account", "id", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"That account could not be read",
			"The database reported an error while reading account "+strconv.FormatInt(id, 10)+".",
			"Reload this page. If it keeps failing, open the database file with another SQLite tool to check it, and restore your most recent backup if it is damaged.")
		return
	}

	reg, err := transaction.LoadRegister(r.Context(), s.store, acct)
	if err != nil {
		s.log.Error("load register", "account", acct.Name, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"The register could not be read",
			"The transactions for "+acct.Name+" could not be listed, so no balance is shown. Showing a partial register would be worse than showing none.",
			"Reload this page. If it keeps failing, restore your most recent backup.")
		return
	}

	s.render(w, r, http.StatusOK, "register.gohtml", buildRegisterPage(layout{
		Title:    acct.Name,
		Database: s.store.Path(),
		Version:  s.version,
		Accounts: accounts,
		ActiveID: acct.ID,
	}, reg))
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
		page.Rows = append(page.Rows, registerRow{
			ID:              e.ID,
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
		})
	}
	return page
}
