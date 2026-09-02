// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/money"
)

// accountForm is what the reader typed, kept as text so a page that comes back
// with a problem comes back with the answers still in it.
type accountForm struct {
	Name string

	// Type and Currency are the values the select boxes posted, not domain
	// types. Holding them as text is what lets a refused form redisplay a choice
	// the domain rejected.
	Type     string
	Currency string

	// OpeningBalance is the balance the register starts from, before the first
	// transaction. It is one box rather than the register's two, so unlike an
	// entry it takes a sign: a card that is already owed on opens negative, and
	// there is no second box here to say so.
	OpeningBalance string
}

// newAccountPage is the form for a new account, already formatted.
type newAccountPage struct {
	layout

	Form accountForm

	// FormError is shown above the form, empty until something is refused.
	FormError string

	// Types and Currencies fill the two select boxes. Both come from the
	// packages that own them rather than from a list written out here, which is
	// how a build that knows a currency and a form that offers it stay the same
	// build.
	Types      []accountChoice
	Currencies []accountChoice
}

// accountChoice is one option: the value posted back, and what the reader sees.
type accountChoice struct {
	Value    string
	Label    string
	Selected bool
}

// blankAccountForm is the form as a fresh page shows it.
//
// The currency defaults to one the household already uses, because a second
// account is almost always in the same currency as the first. With no accounts
// yet there is nothing to copy and no way to know, so the box opens on USD --
// a starting point the reader changes in one click, not an assumption about
// where they live.
func blankAccountForm(accounts []account.Account) accountForm {
	form := accountForm{Type: string(account.Checking), Currency: string(money.USD)}

	for _, a := range accounts {
		if !a.IsClosed() {
			form.Currency = string(a.Currency)
			return form
		}
	}
	if len(accounts) > 0 {
		form.Currency = string(accounts[0].Currency)
	}
	return form
}

// parseAccountForm validates what was typed.
//
// It returns the account to create, the form to redisplay, and a problem to show
// above it. A non-empty problem means nothing should be written, and it says
// what to do next as well as what happened (SPECIFICATION.md RG-4).
//
// It is pure, so the rules can be tested without an HTTP request or a database.
func parseAccountForm(v url.Values) (account.New, accountForm, string) {
	form := accountForm{
		Name:           strings.TrimSpace(v.Get("name")),
		Type:           strings.TrimSpace(v.Get("type")),
		Currency:       strings.TrimSpace(v.Get("currency")),
		OpeningBalance: strings.TrimSpace(v.Get("opening_balance")),
	}

	if form.Name == "" {
		return account.New{}, form, "Every account needs a name. Type what you call it — the name on the statement, or simply Checking — then press Create again."
	}

	// The two select boxes can only post values the page offered, so reaching
	// either of these means the request did not come from the page. Say so
	// plainly rather than accepting it.
	acctType := account.Type(form.Type)
	if !acctType.Valid() {
		return account.New{}, form, fmt.Sprintf(
			"%q is not a kind of account this program keeps. Choose one from the list, then press Create again.", form.Type)
	}

	currency := money.Currency(form.Currency)
	if _, known := money.Scale(currency); !known {
		return account.New{}, form, fmt.Sprintf(
			"This build does not know the currency %q, so it could not say how many decimal places the account keeps. Choose one from the list, then press Create again.", form.Currency)
	}

	opening, problem := parseOpeningBalance(form.OpeningBalance, currency)
	if problem != "" {
		return account.New{}, form, problem
	}

	return account.New{
		Name:           form.Name,
		Type:           acctType,
		Currency:       currency,
		OpeningBalance: opening,
	}, form, ""
}

// parseOpeningBalance reads the opening balance box as money in cur. An empty
// box is zero, which is what a new account normally opens at.
func parseOpeningBalance(text string, cur money.Currency) (money.Money, string) {
	if text == "" {
		zero, err := money.Zero(cur)
		if err != nil {
			return money.Money{}, fmt.Sprintf(
				"This build does not know the currency %s. Choose another, then press Create again.", cur)
		}
		return zero, ""
	}

	amount, err := money.ParseDecimal(text, cur)
	if err != nil {
		// Scale is the money package's rule, restated here only to say which
		// rule was broken: a third decimal place is refused in USD and accepted
		// in KWD without this function knowing either.
		if scale, ok := money.Scale(cur); ok {
			if _, frac, hasFrac := strings.Cut(text, "."); hasFrac && len(frac) > scale {
				return money.Money{}, fmt.Sprintf(
					"%s is recorded to %d decimal places, so %q is more precise than this account can hold. Round it, then press Create again.",
					cur, scale, text)
			}
		}
		return money.Money{}, fmt.Sprintf(
			"%q is not an amount. Type digits and at most one decimal point, such as 250.00, or leave the box empty to open at zero, then press Create again.", text)
	}
	return amount, ""
}

// handleNewAccount shows the form for a new account.
func (s *Server) handleNewAccount(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts.",
			"Check that the file exists and is not open in another program, then reload this page. Your records are not changed by reading them.")
		return
	}

	s.renderNewAccount(w, r, http.StatusOK, accounts, blankAccountForm(accounts), "")
}

// handleCreateAccount writes one account and sends the reader to its register.
//
// It answers with a redirect for the same reason entering a transaction does: a
// reload then re-reads the register rather than creating a second account.
func (s *Server) handleCreateAccount(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts, so the account was not created.",
			"Check that the file exists and is not open in another program, then go back and fill the form in again. Nothing was recorded.")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, accounts,
			"That form could not be read",
			"The browser sent a form this program could not decode, so no account was created.",
			"Go back and fill it in again.")
		return
	}

	// PostForm, not Form: only what was typed into the form counts, so a value
	// in the query string cannot stand in for a field.
	n, form, problem := parseAccountForm(r.PostForm)
	if problem != "" {
		s.renderNewAccount(w, r, http.StatusUnprocessableEntity, accounts, form, problem)
		return
	}

	acct, err := account.Create(r.Context(), s.store, n)
	if err != nil {
		s.createAccountFailed(w, r, accounts, form, err)
		return
	}

	// See Other, so the reload that follows is a GET of the new register. The
	// household lands on the account they just made, which is where the first
	// transaction goes.
	http.Redirect(w, r, fmt.Sprintf("/accounts/%d", acct.ID), http.StatusSeeOther)
}

// createAccountFailed answers a refused account.
//
// A name already in use is the reader's to fix, so it comes back on the form
// with what they typed still in it. Everything else below is a rule the form
// already checked, which means the two disagree: it is logged and reported on
// the form rather than as a 500, because the reader can still act on it.
func (s *Server) createAccountFailed(w http.ResponseWriter, r *http.Request, accounts []account.Account, form accountForm, err error) {
	switch {
	case errors.Is(err, account.ErrDuplicateName):
		s.renderNewAccount(w, r, http.StatusUnprocessableEntity, accounts, form, fmt.Sprintf(
			"There is already an account called %q, and names are compared without regard to case. Choose a different name, then press Create again. Nothing was recorded.", form.Name))

	case errors.Is(err, account.ErrMissingName),
		errors.Is(err, account.ErrInvalidType),
		errors.Is(err, money.ErrCurrencyMismatch),
		errors.Is(err, money.ErrInvalidCurrency):
		s.log.Error("create account refused after the form accepted it", "name", form.Name, "err", err)
		s.renderNewAccount(w, r, http.StatusUnprocessableEntity, accounts, form,
			"That account was refused when it was written, and nothing was recorded. Check the name, the kind, and the opening balance, then press Create again.")

	default:
		s.log.Error("create account", "name", form.Name, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"That account could not be written",
			"The database reported an error while creating "+form.Name+".",
			"Reload the account list to see whether it arrived. If it did not, fill the form in again; if the error repeats, restore your most recent backup.")
	}
}

// renderNewAccount writes the form, carrying form and formError into it.
//
// Both the form handler and the create handler end here, so a refused account
// comes back on the page it was typed on rather than on a second page that would
// have to be kept in step with this one.
func (s *Server) renderNewAccount(w http.ResponseWriter, r *http.Request, status int, accounts []account.Account, form accountForm, formError string) {
	page := newAccountPage{
		layout:    s.pageLayout("New account", accounts, 0),
		Form:      form,
		FormError: formError,
	}

	for _, t := range account.Types() {
		page.Types = append(page.Types, accountChoice{
			Value:    string(t),
			Label:    typeLabel(t),
			Selected: string(t) == form.Type,
		})
	}
	for _, c := range money.Currencies() {
		page.Currencies = append(page.Currencies, accountChoice{
			Value:    string(c),
			Label:    string(c),
			Selected: string(c) == form.Currency,
		})
	}

	s.render(w, r, status, "new-account.gohtml", page)
}

// typeLabel is what a kind of account is called on screen. The stored value is
// the schema's, which is lower case and abbreviated; this is the reader's.
func typeLabel(t account.Type) string {
	switch t {
	case account.Checking:
		return "Checking"
	case account.Savings:
		return "Savings"
	case account.Credit:
		return "Credit card"
	case account.Cash:
		return "Cash"
	default:
		return string(t)
	}
}
