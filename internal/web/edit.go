// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// editPage is the form for changing one transaction.
//
// It reuses entryForm, and the reuse is the point: an edit is the same set of
// boxes with the same rules, and a second set would be a second place for the
// two to disagree about what a date or an amount is.
type editPage struct {
	layout

	Account  account.Account
	Currency string

	ID int64

	Form      entryForm
	FormError string

	// Token is the transaction's updated_at as it was read. It goes back with
	// the change, so a form left open while another tab changed the same
	// transaction is refused rather than applied (CO-3).
	Token string

	// IsSplit marks a transaction divided among several categories. This release
	// has no split editor, so the category and the amount are shown and not
	// offered: flattening three categories into one because the form only has
	// one box would be a quiet loss of what the household recorded.
	IsSplit    bool
	SplitCount int

	// Status is shown as a fact rather than offered as a field. Clearing is what
	// the bank did, not a correction to what was typed, and the register's own
	// mark is where it is changed (RC-3).
	StatusLabel string

	Categories []string
}

// handleEditForm shows one transaction in a form.
func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, acct, detail, ok := s.transactionFor(w, r, cb)
	if !ok {
		return
	}

	form := entryForm{
		Date:        detail.Date,
		CheckNumber: detail.CheckNumber,
		Payee:       detail.Payee,
		Memo:        detail.Memo,
		Category:    detail.Category,
	}
	// Back into the two boxes of a paper register. The stored amount carries the
	// sign; the box it goes in is what says which way the money went, so the
	// sign comes off here rather than being shown twice.
	if detail.Amount.IsNegative() {
		form.Payment = detail.Amount.Abs().Decimal()
	} else {
		form.Deposit = detail.Amount.Decimal()
	}

	s.renderEditPage(w, r, cb, http.StatusOK, accounts, acct, detail, form, "")
}

// handleUpdateTransaction writes a change and sends the reader back to the
// register.
//
// A redirect rather than a page, and no htmx: changing a date or an amount moves
// the running balance of every row below it, so there is no fragment to swap and
// a full re-render is the honest answer.
func (s *Server) handleUpdateTransaction(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts, acct, detail, ok := s.transactionFor(w, r, cb)
	if !ok {
		return
	}

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, cb, http.StatusBadRequest, accounts,
			"That change could not be read",
			"The browser sent a form this program could not decode, so nothing was changed.",
			"Go back to the register and open the transaction again.")
		return
	}

	seen, err := storage.ParseTime(r.PostForm.Get("updated_at"))
	if err != nil {
		// Without the token there is nothing to compare against, and a write
		// that skipped the comparison is the silent overwrite CO-3 forbids.
		s.fail(w, r, cb, http.StatusBadRequest, accounts,
			"That change arrived without a version to check",
			"The form did not carry the version of the transaction it was filled in against, so nothing was changed.",
			"Go back to the register and open the transaction again.")
		return
	}

	ent, form, problem := parseEntryForm(r.PostForm, acct, "Save")
	if problem != "" {
		// 422: understood, and refused on its contents. The form comes back with
		// the reader's work still in it.
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, form, problem)
		return
	}

	edit := transaction.Edit{
		Date:        ent.New.Date,
		Payee:       ent.New.Payee,
		Memo:        ent.New.Memo,
		Amount:      ent.New.Amount,
		CheckNumber: ent.New.CheckNumber,
	}

	// A transaction split among several categories keeps them. Splits stays nil,
	// which is Update's "leave them exactly as they are" -- not an empty slice,
	// which would remove them.
	if !detail.IsSplit() {
		splits := []transaction.Split{}
		if ent.Category != "" {
			cat, err := category.Ensure(r.Context(), cb.store, ent.Category)
			if err != nil {
				s.log.Error("ensure category", "name", ent.Category, "err", err)
				s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
					"That category could not be recorded",
					"The database reported an error while looking up the category "+ent.Category+", so nothing was changed.",
					"Go back to the register and try the change again. Nothing was recorded.")
				return
			}
			splits = append(splits, transaction.Split{CategoryID: cat.ID, Amount: ent.New.Amount})
		}
		edit.Splits = &splits
	}

	txn, err := transaction.Update(r.Context(), cb.store, acct, detail.ID, edit, seen)
	if err != nil {
		s.updateFailed(w, r, cb, accounts, acct, detail, form, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/accounts/%d#txn-%d", acct.ID, txn.ID), http.StatusSeeOther)
}

// updateFailed answers a refused change.
func (s *Server) updateFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, accounts []account.Account, acct account.Account, detail transaction.Detail, form entryForm, err error) {
	switch {
	case errors.Is(err, transaction.ErrConflict):
		// Not an error page. The reader's tab is out of date, so they are told
		// what the transaction now says and given the form back with their own
		// work still in it and the current version attached. Pressing Save again
		// then applies their change deliberately, which is what CO-3 asks for:
		// the conflict is reported rather than either edit being discarded
		// quietly.
		current, getErr := transaction.Get(r.Context(), cb.store, acct, detail.ID)
		if getErr != nil {
			s.fail(w, r, cb, http.StatusConflict, accounts,
				"That change was not applied",
				"This transaction was changed in another window while this page was open, so nothing was written.",
				"Reload the register and open the transaction again.")
			return
		}
		s.renderEditPage(w, r, cb, http.StatusConflict, accounts, acct, current, form,
			"This transaction was changed in another window while this form was open, so nothing was written. It now reads "+
				current.Date+", "+current.Payee+", "+formatAmount(current.Amount)+" "+string(acct.Currency)+
				". Your version is still in the boxes below; press Save to apply it over that one, or go back to the register to leave it alone.")

	case errors.Is(err, transaction.ErrReconciled):
		s.fail(w, r, cb, http.StatusConflict, accounts,
			"That transaction was recorded by a reconciliation",
			"A finished reconciliation recorded this transaction as it stands, and the register does not rewrite what a reconciliation recorded. Nothing was changed.",
			"If the statement and the register disagree, the correction belongs in a reconciliation rather than here. Go back to the register.")

	case errors.Is(err, transaction.ErrSplitTotal):
		// The only way to reach this from here: the transaction is split among
		// several categories, the splits were left alone, and the new amount no
		// longer matches them. A single-category transaction has its one split
		// rewritten for the whole amount, so it cannot fail this check.
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, form,
			"This transaction is divided among "+strconv.Itoa(detail.SplitCount)+
				" categories, and changing its amount would leave those parts adding up to something else. Nothing was changed. "+
				"This release cannot edit the parts, so change the rest of the transaction and leave the amount as it is, or remove this transaction and enter it again.")

	case errors.Is(err, transaction.ErrNotFound):
		s.fail(w, r, cb, http.StatusNotFound, accounts,
			"No such transaction",
			"There is no transaction numbered "+strconv.FormatInt(detail.ID, 10)+" in "+acct.Name+" any more. A tab left open may be pointing at one that was removed.",
			"Reload the register.")

	case errors.Is(err, transaction.ErrInvalidDate) || errors.Is(err, money.ErrCurrencyMismatch):
		// The form already refused both, so reaching one means the two disagree.
		s.log.Error("update refused after the form accepted it", "account", acct.Name, "transaction", detail.ID, "err", err)
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, form,
			"That change was refused when it was written, and nothing was recorded. Check the date and the amount, then press Save again.")

	default:
		s.log.Error("update transaction", "account", acct.Name, "transaction", detail.ID, "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"That change could not be written",
			"The database reported an error while changing a transaction in "+acct.Name+". The transaction and its categories are changed together or not at all, so the register is not half-changed.",
			"Reload the register to see whether it arrived. If the error repeats, restore your most recent backup.")
	}
}

// renderEditPage writes the form, carrying whatever problem there is to report.
func (s *Server) renderEditPage(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, accounts []account.Account, acct account.Account, detail transaction.Detail, form entryForm, formError string) {
	// The suggestions are a convenience, so a failure to read them is not worth
	// refusing a form the reader can otherwise use.
	categories, err := category.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list categories", "err", err)
	}
	names := make([]string, 0, len(categories))
	for _, c := range categories {
		names = append(names, c.Name)
	}

	s.render(w, r, status, "edit-transaction.gohtml", editPage{
		layout:      s.pageLayout(r, cb, "Edit a transaction", accounts, acct.ID),
		Account:     acct,
		Currency:    string(acct.Currency),
		ID:          detail.ID,
		Form:        form,
		FormError:   formError,
		Token:       storage.FormatTime(detail.UpdatedAt),
		IsSplit:     detail.IsSplit(),
		SplitCount:  detail.SplitCount,
		StatusLabel: statusLabel(detail.Status),
		Categories:  names,
	})
}

// transactionFor reads the account and the transaction named by the request
// path, writing the error page itself if either is missing.
//
// The four routes that change or remove a transaction all begin this way, and
// answering a bad address in four places is how they would drift apart. A
// reconciled transaction is refused here rather than in each of them: RC-3 makes
// it a page none of the four has anything to offer on.
func (s *Server) transactionFor(w http.ResponseWriter, r *http.Request, cb *checkbook) ([]account.Account, account.Account, transaction.Detail, bool) {
	accounts, err := account.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, nil,
			"The account list could not be read",
			"The database at "+cb.path+" reported an error while listing accounts, so nothing was changed.",
			"Reload the register and try again. Nothing was changed.")
		return nil, account.Account{}, transaction.Detail{}, false
	}

	acct, ok := s.accountFor(w, r, cb, accounts)
	if !ok {
		return nil, account.Account{}, transaction.Detail{}, false
	}

	id, err := strconv.ParseInt(r.PathValue("txn"), 10, 64)
	if err != nil {
		s.fail(w, r, cb, http.StatusNotFound, accounts,
			"That is not a transaction address",
			"The address "+r.URL.Path+" does not name a transaction in "+acct.Name+".",
			"Go back to the register and choose the transaction there.")
		return nil, account.Account{}, transaction.Detail{}, false
	}

	detail, err := transaction.Get(r.Context(), cb.store, acct, id)
	if err != nil {
		if errors.Is(err, transaction.ErrNotFound) {
			// Ids are never reused (ST-9), so a missing one means the
			// transaction was removed, not that it became a different one.
			s.fail(w, r, cb, http.StatusNotFound, accounts,
				"No such transaction",
				"There is no transaction numbered "+strconv.FormatInt(id, 10)+" in "+acct.Name+". A bookmark or an open tab may be pointing at one that was removed.",
				"Go back to the register and choose the transaction there.")
			return nil, account.Account{}, transaction.Detail{}, false
		}
		s.log.Error("get transaction", "account", acct.Name, "transaction", id, "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"That transaction could not be read",
			"The database reported an error while reading transaction "+strconv.FormatInt(id, 10)+" in "+acct.Name+".",
			"Reload the register. If it keeps failing, restore your most recent backup.")
		return nil, account.Account{}, transaction.Detail{}, false
	}

	if detail.Status == transaction.Reconciled {
		s.fail(w, r, cb, http.StatusConflict, accounts,
			"That transaction was recorded by a reconciliation",
			"A finished reconciliation recorded this transaction as it stands, and the register does not change or remove what a reconciliation recorded (RC-3). Nothing was changed.",
			"If the statement and the register disagree, the correction belongs in a reconciliation rather than here. Go back to the register.")
		return nil, account.Account{}, transaction.Detail{}, false
	}

	return accounts, acct, detail, true
}
