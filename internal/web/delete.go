// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// removedParam marks a register reached after a transaction was removed, so the
// page says so. It carries no text from the request: the message is fixed here,
// the way every other notice's is, so an address cannot put words on the page.
const removedParam = "removed"

// deletePage asks before removing a transaction (RG-3).
type deletePage struct {
	layout

	Account  account.Account
	Currency string

	ID          int64
	Date        string
	CheckNumber string
	Payee       string
	Memo        string
	Category    string
	Amount      string
	StatusLabel string

	// IsSplit says the transaction is divided among several categories, all of
	// which go with it. The page names them by number rather than pretending
	// there is one.
	IsSplit    bool
	SplitCount int

	// Token is the version the removal is made against (CO-3).
	Token string
}

// handleConfirmDelete asks whether to remove a transaction.
//
// Removing one is destructive and irreversible from inside the program, so it is
// asked about rather than done on one press (RG-3). The page shows the whole
// transaction, because "are you sure?" over a row the reader cannot see is not a
// question they can answer.
func (s *Server) handleConfirmDelete(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, acct, detail, ok := s.transactionFor(w, r, cb)
	if !ok {
		return
	}

	label := detail.Category
	if detail.IsSplit() {
		label = "Split " + strconv.Itoa(detail.SplitCount) + " ways"
	} else if label == "" {
		label = "Uncategorized"
	}

	s.render(w, r, http.StatusOK, "delete-transaction.gohtml", deletePage{
		layout:      s.pageLayout(r, cb, "Remove a transaction", accounts, acct.ID),
		Account:     acct,
		Currency:    string(acct.Currency),
		ID:          detail.ID,
		Date:        detail.Date,
		CheckNumber: detail.CheckNumber,
		Payee:       detail.Payee,
		Memo:        detail.Memo,
		Category:    label,
		Amount:      formatAmount(detail.Amount),
		StatusLabel: statusLabel(detail.Status),
		IsSplit:     detail.IsSplit(),
		SplitCount:  detail.SplitCount,
		Token:       storage.FormatTime(detail.UpdatedAt),
	})
}

// handleDeleteTransaction removes one.
func (s *Server) handleDeleteTransaction(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, acct, detail, ok := s.transactionFor(w, r, cb)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, cb, http.StatusBadRequest, accounts,
			"That removal could not be read",
			"The browser sent a form this program could not decode, so nothing was removed.",
			"Go back to the register and try again.")
		return
	}

	seen, err := storage.ParseTime(r.PostForm.Get("updated_at"))
	if err != nil {
		s.fail(w, r, cb, http.StatusBadRequest, accounts,
			"That removal arrived without a version to check",
			"The form did not carry the version of the transaction it was made against, so nothing was removed.",
			"Go back to the register and try again.")
		return
	}

	if err := transaction.Delete(r.Context(), cb.store, acct, detail.ID, seen); err != nil {
		s.deleteFailed(w, r, cb, accounts, acct, detail.ID, err)
		return
	}

	// See Other, so the reload that follows is a GET of the register. There is
	// no fragment to return to: the row it named is gone.
	http.Redirect(w, r, fmt.Sprintf("/accounts/%d?%s=1", acct.ID, removedParam), http.StatusSeeOther)
}

// deleteFailed answers a refused removal.
func (s *Server) deleteFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, accounts []account.Account, acct account.Account, id int64, err error) {
	switch {
	case errors.Is(err, transaction.ErrConflict):
		// The reader confirmed a transaction that has since changed. Removing it
		// anyway would remove something they were not shown, so they are sent
		// back to look at it as it now stands (CO-3).
		s.fail(w, r, cb, http.StatusConflict, accounts,
			"That transaction was not removed",
			"It was changed in another window while this page was open, so it is no longer the transaction that was confirmed. Nothing was removed.",
			"Go back to the register and look at it as it now stands. If you still want it gone, remove it again from there.")

	case errors.Is(err, transaction.ErrReconciled):
		s.fail(w, r, cb, http.StatusConflict, accounts,
			"That transaction was recorded by a reconciliation",
			"A finished reconciliation recorded this transaction, and the register does not remove what a reconciliation recorded. Nothing was removed.",
			"If the statement and the register disagree, the correction belongs in a reconciliation rather than here. Go back to the register.")

	case errors.Is(err, transaction.ErrNotFound):
		// Already gone, and ids are never reused (ST-9), so this cannot have
		// landed on a different transaction.
		s.fail(w, r, cb, http.StatusNotFound, accounts,
			"That transaction is already gone",
			"There is no transaction numbered "+strconv.FormatInt(id, 10)+" in "+acct.Name+". Another window may have removed it already.",
			"Reload the register.")

	default:
		s.log.Error("delete transaction", "account", acct.Name, "transaction", id, "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"That transaction could not be removed",
			"The database reported an error while removing a transaction from "+acct.Name+". The transaction and its categories go together or not at all, so the register is not half-changed.",
			"Reload the register to see whether it is still there. If the error repeats, restore your most recent backup.")
	}
}
