// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
	"github.com/mdhender/mmm/internal/web"
)

func open(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.OpenMemory(t.Context(), strings.ReplaceAll(t.Name(), "/", "-"))
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { _ = s.Close() }) })
	return s
}

// server returns a handler over store. Log output is discarded: these tests
// exercise failures on purpose and the noise is not the subject.
func server(t *testing.T, store *storage.Store) http.Handler {
	t.Helper()
	s, err := web.New(store, "0.0.0-test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	return s
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func usd(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.ParseDecimal(amount, money.USD)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", amount, err)
	}
	return m
}

// seed builds a small register: one reconciled deposit, one cleared split
// purchase, one uncleared uncategorized purchase.
func seed(t *testing.T, store *storage.Store) account.Account {
	t.Helper()

	acct, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD,
		OpeningBalance: usd(t, "3812.44"),
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	groceries, err := category.Ensure(t.Context(), store, "Groceries")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	household, err := category.Ensure(t.Context(), store, "Household")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	for _, n := range []transaction.New{
		{Date: "2026-08-14", Payee: "Acme Manufacturing", Amount: usd(t, "2480.16"), Status: transaction.Reconciled},
		{
			Date: "2026-08-27", Payee: "Riba Smith", Amount: usd(t, "-84.17"), Status: transaction.Cleared,
			Splits: []transaction.Split{
				{CategoryID: groceries.ID, Amount: usd(t, "-71.22")},
				{CategoryID: household.ID, Amount: usd(t, "-12.95")},
			},
		},
		{Date: "2026-08-30", Payee: "Panaderia Ana", Amount: usd(t, "-14.75")},
	} {
		if _, err := transaction.Create(t.Context(), store, acct, n); err != nil {
			t.Fatalf("create transaction %q: %v", n.Payee, err)
		}
	}
	return acct
}

// TestRegisterPage checks that the register shows every column RG-1 requires,
// with the running balance and both totals.
func TestRegisterPage(t *testing.T) {
	store := open(t)
	acct := seed(t, store)

	w := get(t, server(t, store), "/accounts/1")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	body := w.Body.String()
	for _, want := range []string{
		acct.Name,
		// A calendar date is shown as entered, never shifted into a timezone.
		"2026-08-14", "Acme Manufacturing",
		"2,480.16", "6,292.60", // amount and running balance, grouped
		"-84.17", "6,208.43",
		"— Split —",       // several categories name none of them
		"Uncategorized",   // and no category says so rather than showing blank
		"6,193.68",        // ending balance
		"6,208.43",        // cleared balance
		"Cleared balance", // RC-1: both are on screen
		"Not yet cleared",
		":memory:", // BK-3: the database in use is named
	} {
		if !strings.Contains(body, want) {
			t.Errorf("register page does not contain %q", want)
		}
	}
}

// TestRegisterEscapesPayee guards the one injection route a local, single-user
// page still has: text the household typed itself, or imported from a bank file.
func TestRegisterEscapesPayee(t *testing.T) {
	store := open(t)

	acct, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-08-14", Payee: `<script>alert("x")</script>`, Amount: usd(t, "-1.00"),
	}); err != nil {
		t.Fatalf("create transaction: %v", err)
	}

	body := get(t, server(t, store), "/accounts/1").Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Error("payee was written to the page unescaped")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("payee is missing from the page entirely")
	}
}

func TestRootRedirectsToFirstAccount(t *testing.T) {
	store := open(t)
	seed(t, store)

	w := get(t, server(t, store), "/")
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if got := w.Header().Get("Location"); got != "/accounts/1" {
		t.Errorf("Location = %q, want /accounts/1", got)
	}
}

func TestRootWithNoAccounts(t *testing.T) {
	store := open(t)

	w := get(t, server(t, store), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No accounts yet") {
		t.Error("empty database does not explain that there are no accounts")
	}
}

// TestErrorPagesSayWhatToDoNext is RG-4 as a test: every failure page names the
// next safe step, not just the problem.
func TestErrorPagesSayWhatToDoNext(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name string
		path string
		want string
	}{
		{"unparseable id", "/accounts/nope", "not an account address"},
		{"missing account", "/accounts/99", "No such account"},
		{"unknown page", "/reports", "no page at that address"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := get(t, h, tt.path)
			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("page does not say what happened: want %q", tt.want)
			}
			if !strings.Contains(body, "What to do next") {
				t.Error("page does not say what to do next")
			}
		})
	}
}

// TestWriteMethodsAreRejected: nothing on these pages writes yet, and the mux
// says so rather than a handler quietly treating a POST as a GET.
func TestWriteMethodsAreRejected(t *testing.T) {
	store := open(t)
	seed(t, store)

	w := httptest.NewRecorder()
	server(t, store).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/accounts/1", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", w.Code)
	}
}

func TestStylesheetIsServed(t *testing.T) {
	store := open(t)

	w := get(t, server(t, store), "/static/app.css")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", got)
	}
}
