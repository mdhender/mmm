// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

// accountValues is a complete, valid account. Tests change one field at a time
// so each failure names exactly one rule.
func accountValues() url.Values {
	return url.Values{
		"name":            {"Household Checking"},
		"type":            {"checking"},
		"currency":        {"USD"},
		"opening_balance": {"3812.44"},
	}
}

// accounts reads the account list straight from the domain, so an assertion
// about what was written does not depend on how the page renders it.
func accounts(t *testing.T, store *storage.Store) []account.Account {
	t.Helper()
	list, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return list
}

// TestCreateAccount is the whole point of the step: an empty database can be
// given its first account from the browser, and the reader lands on its
// register.
func TestCreateAccount(t *testing.T) {
	store := open(t)
	h := server(t, store)

	w := post(t, h, "/accounts", accountValues())
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/accounts/1" {
		t.Errorf("Location = %q, want /accounts/1", got)
	}

	list := accounts(t, store)
	if len(list) != 1 {
		t.Fatalf("accounts = %d, want 1", len(list))
	}
	got := list[0]
	if got.Name != "Household Checking" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Type != account.Checking {
		t.Errorf("type = %q", got.Type)
	}
	if got.Currency != money.USD {
		t.Errorf("currency = %q", got.Currency)
	}
	if got.OpeningBalance.Decimal() != "3812.44" {
		t.Errorf("opening balance = %q, want 3812.44", got.OpeningBalance.Decimal())
	}
	if got.IsClosed() {
		t.Error("a new account is open")
	}

	// The register it redirected to is a working, empty one.
	body := get(t, h, "/accounts/1").Body.String()
	for _, want := range []string{"Household Checking", "no transactions yet", "3,812.44"} {
		if !strings.Contains(body, want) {
			t.Errorf("register does not contain %q", want)
		}
	}
}

// TestCreateAccountOpensAtZero: the opening balance is the one optional field,
// because most accounts start at nothing.
func TestCreateAccountOpensAtZero(t *testing.T) {
	store := open(t)

	form := accountValues()
	form.Set("opening_balance", "")
	if w := post(t, server(t, store), "/accounts", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", w.Code, w.Body.String())
	}

	got := accounts(t, store)[0]
	if !got.OpeningBalance.IsZero() || got.OpeningBalance.Currency() != money.USD {
		t.Errorf("opening balance = %v, want zero USD", got.OpeningBalance)
	}
}

// TestCreateAccountTakesANegativeOpeningBalance is why this form has one amount
// box and not the register's two: a card is opened owing money, and the sign is
// the only way to say so.
func TestCreateAccountTakesANegativeOpeningBalance(t *testing.T) {
	store := open(t)

	form := accountValues()
	form.Set("name", "Visa")
	form.Set("type", "credit")
	form.Set("opening_balance", "-482.10")
	if w := post(t, server(t, store), "/accounts", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", w.Code, w.Body.String())
	}

	got := accounts(t, store)[0]
	if got.Type != account.Credit {
		t.Errorf("type = %q, want credit", got.Type)
	}
	if got.OpeningBalance.Decimal() != "-482.10" {
		t.Errorf("opening balance = %q, want -482.10", got.OpeningBalance.Decimal())
	}
}

// TestCreateAccountInAnotherCurrency: the currency is the account's, and the
// scale it is held to is the currency's, not one this program restates.
func TestCreateAccountInAnotherCurrency(t *testing.T) {
	store := open(t)

	form := accountValues()
	form.Set("currency", "JPY")
	form.Set("opening_balance", "125000")
	if w := post(t, server(t, store), "/accounts", form); w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", w.Code, w.Body.String())
	}

	got := accounts(t, store)[0]
	if got.Currency != money.JPY {
		t.Errorf("currency = %q, want JPY", got.Currency)
	}
	if got.OpeningBalance.Decimal() != "125000" {
		t.Errorf("opening balance = %q, want 125000", got.OpeningBalance.Decimal())
	}
}

func TestAccountRefusals(t *testing.T) {
	for _, tt := range []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"no name", "name", "", "Every account needs a name"},
		{"unknown kind", "type", "brokerage", "not a kind of account"},
		{"unknown currency", "currency", "XYZ", "does not know the currency"},
		{"opening balance is not a number", "opening_balance", "about 400", "is not an amount"},
		{"opening balance too precise", "opening_balance", "10.005", "more precise than this account can hold"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := open(t)

			form := accountValues()
			form.Set(tt.field, tt.value)
			w := post(t, server(t, store), "/accounts", form)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("page does not say what happened: want %q", tt.want)
			}
			// RG-4: every refusal names the next step. On a form that is the
			// button to press once the answer is fixed.
			if !strings.Contains(body, "press Create again") {
				t.Error("page does not say what to do next")
			}
			if list := accounts(t, store); len(list) != 0 {
				t.Errorf("accounts = %d, want none: a refused form writes nothing", len(list))
			}
		})
	}
}

// TestDuplicateAccountNameIsRefused: the schema compares names without regard to
// case, so two accounts cannot differ only in capitalization. That is a rule the
// reader can act on, so it comes back on the form rather than as a 500.
func TestDuplicateAccountNameIsRefused(t *testing.T) {
	store := open(t)
	h := server(t, store)

	if w := post(t, h, "/accounts", accountValues()); w.Code != http.StatusSeeOther {
		t.Fatalf("first create: status = %d, want 303", w.Code)
	}

	form := accountValues()
	form.Set("name", "household checking")
	w := post(t, h, "/accounts", form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "already an account called") {
		t.Error("page does not say the name is taken")
	}
	if !strings.Contains(body, "press Create again") {
		t.Error("page does not say what to do next")
	}
	if list := accounts(t, store); len(list) != 1 {
		t.Fatalf("accounts = %d, want 1", len(list))
	}
}

// TestRefusedAccountKeepsWhatWasTyped: a form that throws the answers away makes
// the reader pay twice for one mistake.
func TestRefusedAccountKeepsWhatWasTyped(t *testing.T) {
	store := open(t)

	form := accountValues()
	form.Set("name", "Rainy Day Savings")
	form.Set("type", "savings")
	form.Set("currency", "GBP")
	form.Set("opening_balance", "not a number")
	w := post(t, server(t, store), "/accounts", form)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		`value="Rainy Day Savings"`,
		`value="not a number"`,
		`value="savings" selected`,
		`value="GBP" selected`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("refused form does not carry %q back", want)
		}
	}
}

func TestNewAccountFormEscapesWhatWasTyped(t *testing.T) {
	store := open(t)

	form := accountValues()
	form.Set("name", `<script>alert("x")</script>`)
	form.Set("opening_balance", "not a number")
	body := post(t, server(t, store), "/accounts", form).Body.String()

	if strings.Contains(body, "<script>alert") {
		t.Error("the name was written back to the page unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("the name is missing from the redisplayed form")
	}
}

// TestNewAccountFormOffersEveryKindAndCurrency: the boxes come from the packages
// that own the lists, so a build that knows a currency offers it.
func TestNewAccountFormOffersEveryKindAndCurrency(t *testing.T) {
	store := open(t)

	w := get(t, server(t, store), "/accounts/new")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	for _, tt := range account.Types() {
		if !strings.Contains(body, `value="`+string(tt)+`"`) {
			t.Errorf("form does not offer the account type %q", tt)
		}
	}
	for _, c := range money.Currencies() {
		if !strings.Contains(body, `value="`+string(c)+`"`) {
			t.Errorf("form does not offer the currency %q", c)
		}
	}
	// USD on an empty database: a starting point, not an assumption. The reader
	// changes it in one click.
	if !strings.Contains(body, `value="USD" selected`) {
		t.Error("form does not open on a currency")
	}
}

// TestNewAccountFormFollowsTheHouseholdCurrency: a second account is almost
// always in the same currency as the first, so the box opens on that one rather
// than on a default the household has already told the program is wrong.
func TestNewAccountFormFollowsTheHouseholdCurrency(t *testing.T) {
	store := open(t)

	if _, err := account.Create(t.Context(), store, account.New{
		Name: "Current", Type: account.Checking, Currency: money.GBP,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	body := get(t, server(t, store), "/accounts/new").Body.String()
	if !strings.Contains(body, `value="GBP" selected`) {
		t.Error("form does not open on the currency the household already uses")
	}
}

// TestNewAccountIsNotAnAccountAddress: /accounts/new is more specific than
// /accounts/{id}, so it reaches the form rather than the register's "that is not
// an account address".
func TestNewAccountIsNotAnAccountAddress(t *testing.T) {
	store := open(t)
	seed(t, store)

	w := get(t, server(t, store), "/accounts/new")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "New account") {
		t.Error("/accounts/new does not serve the form")
	}
}

// TestEveryPageOffersToAddAnAccount: the account list sits beside every page, so
// the way to add one is there too -- including on the empty database, which
// would otherwise be a dead end (RG-4).
func TestEveryPageOffersToAddAnAccount(t *testing.T) {
	store := open(t)
	h := server(t, store)

	empty := get(t, h, "/").Body.String()
	if !strings.Contains(empty, "/accounts/new") {
		t.Error("an empty database does not offer a way to add an account")
	}

	seed(t, store)
	register := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(register, "/accounts/new") {
		t.Error("the register does not offer a way to add an account")
	}
}
