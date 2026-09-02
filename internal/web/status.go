// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// handleSetStatus marks one transaction cleared, or not cleared again.
//
// This is the first interaction that replaces part of a page rather than all of
// it, and the first that writes to a record somebody else may be holding. Both
// of those show up here: the answer is one row plus the totals, and the write
// carries the token the row was read with (CO-3).
func (s *Server) handleSetStatus(w http.ResponseWriter, r *http.Request) {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.fail(w, r, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+s.store.Path()+" reported an error while listing accounts, so nothing was marked.",
			"Reload the register and try again. Nothing was changed.")
		return
	}

	acct, ok := s.accountFor(w, r, accounts)
	if !ok {
		return
	}

	id, err := strconv.ParseInt(r.PathValue("txn"), 10, 64)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, accounts,
			"That is not a transaction address",
			"The address "+r.URL.Path+" does not name a transaction in "+acct.Name+".",
			"Go back to the register and try the mark again.")
		return
	}

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, accounts,
			"That change could not be read",
			"The browser sent a form this program could not decode, so nothing was marked.",
			"Reload the register and try again.")
		return
	}

	status := transaction.Status(r.PostForm.Get("status"))
	seen, err := storage.ParseTime(r.PostForm.Get("updated_at"))
	if err != nil {
		// Without the token there is nothing to compare against, and a write
		// that skipped the comparison is the silent overwrite CO-3 forbids.
		s.fail(w, r, http.StatusBadRequest, accounts,
			"That change arrived without a version to check",
			"The mark did not carry the version of the transaction it was made against, so it was not applied. Nothing was changed.",
			"Reload the register and mark it again.")
		return
	}

	txn, err := transaction.SetStatus(r.Context(), s.store, acct, id, status, seen)
	if err != nil {
		s.setStatusFailed(w, r, accounts, acct, id, err)
		return
	}

	s.afterStatusChange(w, r, accounts, acct, txn.ID, "")
}

// setStatusFailed answers a refused mark.
//
// A conflict is not an error page. The reader's tab is simply out of date, so it
// is given the row as it now stands together with a notice saying what happened
// -- which is what CO-3 asks for: detect the conflict and say so, rather than
// discard either edit quietly.
func (s *Server) setStatusFailed(w http.ResponseWriter, r *http.Request, accounts []account.Account, acct account.Account, id int64, err error) {
	switch {
	case errors.Is(err, transaction.ErrConflict):
		s.afterStatusChange(w, r, accounts, acct, id,
			"This transaction was changed in another window while this page was open, so the mark was not applied. The row now shows the current value; mark it again if it is still what you want.")

	case errors.Is(err, transaction.ErrReconciled):
		s.afterStatusChange(w, r, accounts, acct, id,
			"That transaction was recorded by a completed reconciliation, so the register does not change it. Undo it in a reconciliation rather than here.")

	case errors.Is(err, transaction.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, accounts,
			"No such transaction",
			"There is no transaction numbered "+strconv.FormatInt(id, 10)+" in "+acct.Name+". A tab left open may be pointing at one that was removed.",
			"Reload the register.")

	case errors.Is(err, transaction.ErrInvalidStatus):
		s.fail(w, r, http.StatusBadRequest, accounts,
			"That is not a mark the register makes",
			"The register marks a transaction cleared or not cleared. Reconciled is recorded by a completed reconciliation and is not set here.",
			"Reload the register and use the mark in the status column.")

	default:
		s.log.Error("set status", "account", acct.Name, "transaction", id, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"That mark could not be written",
			"The database reported an error while marking a transaction in "+acct.Name+".",
			"Reload the register to see whether it was applied. If the error repeats, restore your most recent backup.")
	}
}

// afterStatusChange answers a mark: one row and the totals for htmx, or the
// whole register for a browser that posted the form itself.
//
// The register is reloaded either way rather than patched in memory. The cleared
// balance and the uncleared count both move, and recomputing them here from the
// same code that renders the page is what keeps the fragment and a later reload
// from disagreeing (TS-2).
func (s *Server) afterStatusChange(w http.ResponseWriter, r *http.Request, accounts []account.Account, acct account.Account, id int64, notice string) {
	// Not an htmx request: no script, or it failed to load. The form posted
	// normally, so answer the way the entry form does -- a redirect, so a reload
	// does not repeat the mark.
	if r.Header.Get("HX-Request") != "true" {
		if notice != "" {
			// A full-page answer has nowhere to put an out-of-band notice, so the
			// conflict gets a page of its own rather than being dropped.
			s.fail(w, r, http.StatusConflict, accounts,
				"That mark was not applied",
				notice,
				"Reload the register to see the transaction as it now stands, then mark it again if that is still what you want.")
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/accounts/%d#txn-%d", acct.ID, id), http.StatusSeeOther)
		return
	}

	reg, err := transaction.LoadRegister(r.Context(), s.store, acct)
	if err != nil {
		s.log.Error("load register", "account", acct.Name, "err", err)
		http.Error(w, "the register could not be read; reload the page", http.StatusInternalServerError)
		return
	}

	page := buildRegisterPage(layout{Title: acct.Name}, reg)
	page.RowNotice = notice
	page.OOB = true

	var row registerRow
	found := false
	for _, candidate := range page.Rows {
		if candidate.ID == id {
			row, found = candidate, true
			break
		}
	}
	if !found {
		// The transaction went away between the write and this read. Reloading
		// is the only honest answer.
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusOK)
		return
	}

	// Buffered for the same reason a page is: a template error must not arrive
	// as half a row swapped into somebody's register.
	var buf bytes.Buffer
	t := s.pages["register.gohtml"]
	for _, part := range []struct {
		name string
		data any
	}{
		{"row", row},
		{"totals", page},
		{"notice", page},
	} {
		if err := t.ExecuteTemplate(&buf, part.name, part.data); err != nil {
			s.log.Error("render fragment", "part", part.name, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Debug("write fragment", "path", r.URL.Path, "err", err)
	}
}
