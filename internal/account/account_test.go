// Copyright (c) 2026 Michael D Henderson.

package account_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
)

// open returns a Store over a private in-memory database.
func open(t *testing.T) *storage.Store {
	t.Helper()
	// A subtest's name contains "/", which OpenMemory rejects because a name
	// goes into a database URI.
	s, err := storage.OpenMemory(t.Context(), strings.ReplaceAll(t.Name(), "/", "-"))
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = s.Close() }) })
	return s
}

func usd(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.ParseDecimal(amount, money.USD)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", amount, err)
	}
	return m
}

func TestCreateRoundTrips(t *testing.T) {
	store := open(t)

	want := account.New{
		Name:           "Checking",
		Type:           account.Checking,
		Currency:       money.USD,
		OpeningBalance: usd(t, "3812.44"),
	}
	created, err := account.Create(t.Context(), store, want)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Error("Create returned id 0")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("Create left timestamps unset: created %v, updated %v", created.CreatedAt, created.UpdatedAt)
	}
	if created.IsClosed() {
		t.Errorf("a new account reports itself closed on %q", created.ClosedOn)
	}

	got, err := account.Get(t.Context(), store, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != created {
		t.Errorf("Get = %+v, want %+v", got, created)
	}
	// The opening balance must survive as an integer count of minor units. A
	// float or a "USD 3812.44" string would read back as something else.
	if got.OpeningBalance.Amount() != 381244 {
		t.Errorf("opening balance stored as %d minor units, want 381244", got.OpeningBalance.Amount())
	}
}

func TestCreateDefaultsOpeningBalanceToZero(t *testing.T) {
	store := open(t)

	got, err := account.Create(t.Context(), store, account.New{
		Name: "Cash", Type: account.Cash, Currency: money.USD,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !got.OpeningBalance.IsZero() || got.OpeningBalance.Currency() != money.USD {
		t.Errorf("opening balance = %v, want zero USD", got.OpeningBalance)
	}
}

func TestCreateRejects(t *testing.T) {
	tests := []struct {
		name string
		in   account.New
		want error
	}{
		{"no name", account.New{Type: account.Checking, Currency: money.USD}, account.ErrMissingName},
		{"unknown type", account.New{Name: "X", Type: "brokerage", Currency: money.USD}, account.ErrInvalidType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := open(t)
			if _, err := account.Create(t.Context(), store, tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("Create = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestCreateRejectsForeignOpeningBalance guards the invariant that makes
// SUM(amount) meaningful: the currency lives on the account, so an amount in
// another one cannot be stored against it.
func TestCreateRejectsForeignOpeningBalance(t *testing.T) {
	store := open(t)

	eur, err := money.ParseDecimal("10.00", money.EUR)
	if err != nil {
		t.Fatalf("ParseDecimal: %v", err)
	}
	_, err = account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD, OpeningBalance: eur,
	})
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Create = %v, want ErrCurrencyMismatch", err)
	}
}

// TestCreateRefusesADuplicateName guards the rule the schema encodes with
// COLLATE NOCASE: "Checking" and "checking" are one account, not two that would
// split a household's records between them. The sentinel is what lets the
// interface say so on the form rather than reporting a database error.
func TestCreateRefusesADuplicateName(t *testing.T) {
	store := open(t)

	first := account.New{Name: "Checking", Type: account.Checking, Currency: money.USD}
	if _, err := account.Create(t.Context(), store, first); err != nil {
		t.Fatalf("Create: %v", err)
	}

	second := account.New{Name: "checking", Type: account.Savings, Currency: money.USD}
	if _, err := account.Create(t.Context(), store, second); !errors.Is(err, account.ErrDuplicateName) {
		t.Fatalf("Create = %v, want ErrDuplicateName", err)
	}

	list, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("accounts = %d, want 1", len(list))
	}
}

// TestTypesAreAllValid keeps the list a form offers and the set the schema
// accepts from drifting apart.
func TestTypesAreAllValid(t *testing.T) {
	store := open(t)

	types := account.Types()
	if len(types) == 0 {
		t.Fatal("Types is empty")
	}
	for _, tt := range types {
		if !tt.Valid() {
			t.Errorf("Types offers %q, which Valid rejects", tt)
		}
		// One store, so the accounts are named apart: the name is unique.
		if _, err := account.Create(t.Context(), store, account.New{
			Name: string(tt), Type: tt, Currency: money.USD,
		}); err != nil {
			t.Errorf("Create with type %q: %v", tt, err)
		}
	}
}

func TestGetMissingAccount(t *testing.T) {
	store := open(t)
	if _, err := account.Get(t.Context(), store, 404); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

// TestListOrdersOpenAccountsFirst pins the order the account list depends on.
func TestListOrdersOpenAccountsFirst(t *testing.T) {
	store := open(t)

	for _, name := range []string{"Visa", "Checking", "Savings"} {
		if _, err := account.Create(t.Context(), store, account.New{
			Name: name, Type: account.Checking, Currency: money.USD,
		}); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}

	got, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"Checking", "Savings", "Visa"}
	if len(got) != len(want) {
		t.Fatalf("List returned %d accounts, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("List[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestListEmptyDatabase(t *testing.T) {
	store := open(t)
	got, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List returned %d accounts, want 0", len(got))
	}
}
