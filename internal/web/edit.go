// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/category"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
)

// The buttons on the edit form, as the button that was pressed names itself.
//
// Two of them reshape the form and write nothing; anything else is Save. The
// name is read from the posted form rather than remembered in the browser,
// because the mode this page is in has to survive a page that comes back with a
// problem on it.
const (
	actionSplit   = "split"
	actionAddLine = "add-line"

	// actionTally re-adds the parts and answers with the tally alone. It is what
	// a changed box asks for, and like the other two it writes nothing.
	actionTally = "tally"
)

// The fragments a reshaping press is answered with, named as the templates that
// render them.
const (
	editFormFragment   = "edit-form"
	splitTallyFragment = "split-tally"
)

// splitLine is one line of the split editor as it was typed.
//
// The amount is text and unsigned, like the two amount boxes above it: the
// transaction already says which way the money went, so a line has no sign to
// get wrong (ADR 1).
type splitLine struct {
	Category string
	Memo     string
	Amount   string
}

// editState is what the reader has in front of them: the boxes, the mode, the
// lines, and the version all of it was filled in against.
//
// It is carried through a re-render whole. A form that comes back with a problem
// on it and has quietly changed mode, lost a line, or picked up a fresher token
// than the one the reader was working against would be answering a question
// nobody asked (CO-3).
type editState struct {
	Form  entryForm
	Split bool
	Lines []splitLine
	Token string
}

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

	// Split is the form's mode: one Category box, or a line per part. It is a
	// field on the form rather than state held in the browser, so the mode
	// survives a page that has to come back with a problem on it, and so the
	// editor works with no script at all.
	Split bool
	Lines []splitLine

	// Tally is the three figures under the lines. The gap is shown while it is
	// being made rather than discovered on Save (RC-2's spirit: the discrepancy
	// is the thing to show).
	Tally tally

	// Status is shown as a fact rather than offered as a field. Clearing is what
	// the bank did, not a correction to what was typed, and the register's own
	// mark is where it is changed (RC-3).
	StatusLabel string

	Categories []string
}

// tally is the three figures under the lines, ready to print.
//
// They are computed in Go from money, never in a template (CO-1), and they are
// recomputed on every render -- including the one Add a line causes -- which is
// what makes the gap feel live without anything calculating in the browser.
type tally struct {
	// Amount is the transaction's amount as the boxes hold it: unsigned, like
	// everything else on this form.
	Amount string

	// Assigned is what the lines add up to and Unassigned is what is left.
	// Unassigned is negative when the lines assign more than there is.
	Assigned   string
	Unassigned string
}

// unknownFigure stands in for a figure that cannot be computed because something
// typed is not an amount yet. The box that is wrong says so itself when Save is
// pressed; the tally does not guess at what it might have meant.
const unknownFigure = "—"

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
	}
	// Back into the two boxes of a paper register. The stored amount carries the
	// sign; the box it goes in is what says which way the money went, so the
	// sign comes off here rather than being shown twice.
	if detail.Amount.IsNegative() {
		form.Payment = detail.Amount.Abs().Decimal()
	} else {
		form.Deposit = detail.Amount.Decimal()
	}

	st := editState{Form: form, Token: storage.FormatTime(detail.UpdatedAt)}
	if shownPlainly(detail) {
		if len(detail.Splits) == 1 {
			st.Form.Category = detail.Splits[0].Category
		}
	} else {
		st.Split = true
		st.Lines = linesOf(detail.Splits)
	}

	s.renderEditPage(w, r, cb, http.StatusOK, accounts, acct, detail, st, "")
}

// handleUpdateTransaction writes a change and sends the reader back to the
// register.
//
// A redirect rather than a page, and no htmx: changing a date or an amount moves
// the running balance of every row below it, so there is no fragment to swap and
// a full re-render is the honest answer. The two buttons that only reshape the
// form are the exception, and they write nothing.
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

	// The token that goes back on the page is the one that came off it, not the
	// one just read from the database. Nothing here writes, so refreshing it
	// would hand the reader a version they never saw and let their next Save
	// overwrite whatever another window did in between (CO-3).
	st := editState{
		Split: r.PostForm.Get("split") == "1",
		Lines: postedLines(r.PostForm),
		Token: storage.FormatTime(seen),
	}

	// The three presses that only reshape the form take it as typed, whatever
	// state it is in: the reader is still filling it in, and refusing to add a
	// line because the date is half typed would be answering a question they did
	// not ask.
	switch action := r.PostForm.Get("action"); action {
	case actionSplit, actionAddLine:
		_, st.Form, _ = parseEntryForm(r.PostForm, acct, "Save")
		if action == actionSplit {
			st.Lines = seedLines(st.Form, detail)
		} else {
			st.Lines = append(st.Lines, splitLine{})
		}
		st.Split = true
		s.reshapeEditForm(w, r, cb, accounts, acct, detail, st, editFormFragment)
		return

	case actionTally:
		// A box changed. The mode is left exactly as it was posted: this is not
		// a press that decides anything, only one that re-adds what is typed.
		_, st.Form, _ = parseEntryForm(r.PostForm, acct, "Save")
		s.reshapeEditForm(w, r, cb, accounts, acct, detail, st, splitTallyFragment)
		return
	}

	ent, form, problem := parseEntryForm(r.PostForm, acct, "Save")
	st.Form = form
	if problem != "" {
		// 422: understood, and refused on its contents. The form comes back with
		// the reader's work still in it.
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, st, problem)
		return
	}

	splits, problem, err := s.splitsFor(r.Context(), cb, st, ent, detail)
	if problem != "" {
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, st, problem)
		return
	}
	if err != nil {
		s.log.Error("ensure category", "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"That category could not be recorded",
			"The database reported an error while looking up a category, so nothing was changed.",
			"Go back to the register and try the change again. Nothing was recorded.")
		return
	}

	txn, err := transaction.Update(r.Context(), cb.store, acct, detail.ID, transaction.Edit{
		Date:        ent.New.Date,
		Payee:       ent.New.Payee,
		Memo:        ent.New.Memo,
		Amount:      ent.New.Amount,
		CheckNumber: ent.New.CheckNumber,
		Splits:      &splits,
	}, seen)
	if err != nil {
		s.updateFailed(w, r, cb, accounts, acct, detail, st, err)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/accounts/%d#txn-%d", acct.ID, txn.ID), http.StatusSeeOther)
}

// splitsFor turns what the form holds into the parts to store.
//
// It returns a problem to show above the form, or an error if the database could
// not be reached; both mean nothing is written. The parts are always sent, in
// either mode, so the form's own mode decides what the transaction is divided
// into rather than what it happened to be divided into before.
func (s *Server) splitsFor(ctx context.Context, cb *checkbook, st editState, ent entry, detail transaction.Detail) ([]transaction.Split, string, error) {
	if !st.Split {
		// The plain form: one part for the whole amount, or none at all.
		// Uncategorized is a normal state, not an error.
		splits := []transaction.Split{}
		if ent.Category == "" {
			return splits, "", nil
		}
		cat, err := category.Ensure(ctx, cb.store, ent.Category)
		if err != nil {
			return nil, "", fmt.Errorf("category %q: %w", ent.Category, err)
		}
		part := transaction.Split{CategoryID: cat.ID, Amount: ent.New.Amount}
		if len(detail.Splits) == 1 {
			// The one part it already had may carry a memo of its own, written
			// on a line of the split editor. The plain form has nowhere to show
			// it, which is not a reason to throw it away.
			part.Memo = detail.Splits[0].Memo
		}
		return append(splits, part), "", nil
	}

	planned, problem := planSplits(st.Lines, ent.New.Amount)
	if problem != "" {
		return nil, problem, nil
	}
	if len(planned) == 0 {
		// Every line cleared. The transaction is uncategorized, which the model
		// allows and which is how a reader takes back a division they no longer
		// want.
		return []transaction.Split{}, "", nil
	}

	assigned, unassigned, err := reckon(planned, ent.New.Amount)
	if err != nil {
		return nil, "", err
	}
	if !unassigned.IsZero() {
		return nil, remainderProblem(ent.New.Amount, assigned, unassigned), nil
	}

	ids, name, err := ensureCategories(ctx, cb.store, planned)
	if err != nil {
		return nil, "", fmt.Errorf("category %q: %w", name, err)
	}

	splits := make([]transaction.Split, 0, len(planned))
	for _, p := range planned {
		// A line with no category stores NULL, which is CategoryID zero. It is
		// how a reader fills in one line before its neighbour, and how they say
		// that part of this went somewhere they have not decided on yet.
		splits = append(splits, transaction.Split{
			CategoryID: ids[strings.ToLower(p.Category)],
			Amount:     p.Amount,
			Memo:       p.Memo,
		})
	}
	return splits, "", nil
}

// plannedSplit is one line after its amount has been read: the category still by
// name, because resolving a name needs a database this step does not have.
type plannedSplit struct {
	Category string
	Memo     string
	Amount   money.Money
}

// planSplits reads the lines into parts of amount.
//
// A line with a blank amount is dropped, and that is the remove control: a form
// with an emptied line behaving as if the line were not there is what anyone
// would expect, and it beats a button that destroys a line the reader can
// already destroy by emptying it.
//
// Each amount is typed unsigned and stored with the transaction's sign, which is
// ADR 1's rule and the two amount boxes' rule at once.
func planSplits(lines []splitLine, amount money.Money) ([]plannedSplit, string) {
	var planned []plannedSplit
	for i, line := range lines {
		if line.Amount == "" {
			continue
		}
		part, problem := parseSplitAmount(line.Amount, i+1, amount.Currency())
		if problem != "" {
			return nil, problem
		}
		if amount.IsNegative() {
			zero, err := money.Zero(amount.Currency())
			if err == nil {
				part, err = zero.Subtract(part)
			}
			if err != nil {
				return nil, fmt.Sprintf(
					"%s could not be recorded as part of a payment. Report this; it is a fault in the program, not in what you typed.", line.Amount)
			}
		}
		planned = append(planned, plannedSplit{Category: line.Category, Memo: line.Memo, Amount: part})
	}
	return planned, ""
}

// parseSplitAmount reads one line's amount.
//
// The sign and the scale are the entry form's rules, and the wording with them:
// a part of a transaction is typed the way the transaction is, so the same
// function refuses it and says why. Zero is the one thing a line means
// differently -- a part of nothing is not an entry that would not change the
// balance, it is a line the reader meant to fill in or to empty.
func parseSplitAmount(text string, line int, cur money.Currency) (money.Money, string) {
	amount, problem := parseEntryAmount(text, fmt.Sprintf("line %d", line), "Save", cur)
	if problem != "" {
		return money.Money{}, problem
	}
	if amount.IsZero() {
		return money.Money{}, fmt.Sprintf(
			"Line %d is for zero, which assigns nothing to a category. Type an amount on it, or empty the box to take the line away, then press Save again.", line)
	}
	return amount, ""
}

// reckon totals the parts and reports what is left of amount.
//
// Everything is answered unsigned, the way the boxes are typed. Every part
// carries the transaction's sign, so the total carries it too and taking it off
// is the whole of the conversion. Unassigned is negative when the lines assign
// more than there is.
func reckon(planned []plannedSplit, amount money.Money) (assigned, unassigned money.Money, err error) {
	if assigned, err = money.Zero(amount.Currency()); err != nil {
		return money.Money{}, money.Money{}, fmt.Errorf("total the parts: %w", err)
	}
	for _, p := range planned {
		if assigned, err = assigned.Add(p.Amount); err != nil {
			return money.Money{}, money.Money{}, fmt.Errorf("total the parts: %w", err)
		}
	}
	assigned = assigned.Abs()
	if unassigned, err = amount.Abs().Subtract(assigned); err != nil {
		return money.Money{}, money.Money{}, fmt.Errorf("total the parts: %w", err)
	}
	return assigned, unassigned, nil
}

// remainderProblem says the gap in all three numbers.
//
// Naming only the remainder would leave the reader working out which of the two
// numbers behind it they meant to change.
func remainderProblem(amount, assigned, unassigned money.Money) string {
	if unassigned.IsNegative() {
		return fmt.Sprintf(
			"The parts add up to %s and this transaction is %s, so they assign %s more than there is. Nothing was changed. "+
				"Lower a line, or empty one's Amount to take it away, then press Save again.",
			formatAmount(assigned), formatAmount(amount.Abs()), formatAmount(unassigned.Abs()))
	}
	return fmt.Sprintf(
		"The parts add up to %s and this transaction is %s, so %s is still unassigned. Nothing was changed. "+
			"Assign the rest, or empty every line to leave the transaction uncategorized, then press Save again.",
		formatAmount(assigned), formatAmount(amount.Abs()), formatAmount(unassigned))
}

// ensureCategories resolves the names on the lines to ids.
//
// One call per distinct name rather than one per line: two lines naming
// Groceries are one category, and the column is COLLATE NOCASE, so groceries is
// that same one again. The name that failed comes back with the error, because
// a message that cannot say which category could not be recorded is not one the
// reader can act on.
func ensureCategories(ctx context.Context, store *storage.Store, planned []plannedSplit) (map[string]int64, string, error) {
	ids := make(map[string]int64, len(planned))
	for _, p := range planned {
		if p.Category == "" {
			continue
		}
		key := strings.ToLower(p.Category)
		if _, done := ids[key]; done {
			continue
		}
		cat, err := category.Ensure(ctx, store, p.Category)
		if err != nil {
			return nil, p.Category, err
		}
		ids[key] = cat.ID
	}
	return ids, "", nil
}

// updateFailed answers a refused change.
func (s *Server) updateFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, accounts []account.Account, acct account.Account, detail transaction.Detail, st editState, err error) {
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
		st.Token = storage.FormatTime(current.UpdatedAt)
		notice := "This transaction was changed in another window while this form was open, so nothing was written. It now reads " +
			current.Date + ", " + current.Payee + ", " + formatAmount(current.Amount) + " " + string(acct.Currency)
		if current.IsSplit() && !st.Split {
			// The other window divided it. Saving a one-category form over that
			// would replace every part with one, which is a loss worth naming
			// before it happens rather than after (CO-3).
			notice += ", divided among " + strconv.Itoa(current.SplitCount()) +
				" categories, which saving this form would replace with the one in the box above"
		}
		s.renderEditPage(w, r, cb, http.StatusConflict, accounts, acct, current, st,
			notice+". Your version is still in the boxes below; press Save to apply it over that one, or go back to the register to leave it alone.")

	case errors.Is(err, transaction.ErrReconciled):
		s.fail(w, r, cb, http.StatusConflict, accounts,
			"That transaction was recorded by a reconciliation",
			"A finished reconciliation recorded this transaction as it stands, and the register does not rewrite what a reconciliation recorded. Nothing was changed.",
			"If the statement and the register disagree, the correction belongs in a reconciliation rather than here. Go back to the register.")

	case errors.Is(err, transaction.ErrNotFound):
		s.fail(w, r, cb, http.StatusNotFound, accounts,
			"No such transaction",
			"There is no transaction numbered "+strconv.FormatInt(detail.ID, 10)+" in "+acct.Name+" any more. A tab left open may be pointing at one that was removed.",
			"Reload the register.")

	case errors.Is(err, transaction.ErrInvalidDate) ||
		errors.Is(err, transaction.ErrSplitTotal) ||
		errors.Is(err, money.ErrCurrencyMismatch):
		// The form already refused all three -- the parts are totalled against
		// the amount before Update is called -- so reaching one means the two
		// disagree.
		s.log.Error("update refused after the form accepted it", "account", acct.Name, "transaction", detail.ID, "err", err)
		s.renderEditPage(w, r, cb, http.StatusUnprocessableEntity, accounts, acct, detail, st,
			"That change was refused when it was written, and nothing was recorded. Check the date, the amount, and the parts it is divided into, then press Save again.")

	default:
		s.log.Error("update transaction", "account", acct.Name, "transaction", detail.ID, "err", err)
		s.dbFailed(w, r, cb, http.StatusInternalServerError, accounts,
			"That change could not be written",
			"The database reported an error while changing a transaction in "+acct.Name+". The transaction and its categories are changed together or not at all, so the register is not half-changed.",
			"Reload the register to see whether it arrived. If the error repeats, restore your most recent backup.")
	}
}

// reshapeEditForm answers the presses that rearrange the form rather than write
// to the checkbook: Split this transaction, Add a line, and a changed box.
//
// Nothing is written by any of them. htmx swaps the named fragment where the
// script loaded -- the whole form for the first two, the tally alone for the
// third, which is what leaves every box and the caret where they were. Where the
// script did not load, the same POST re-renders the page, which is how every
// other control here works: the tally then refreshes on the next press, as it
// did before there was a trigger at all.
func (s *Server) reshapeEditForm(w http.ResponseWriter, r *http.Request, cb *checkbook, accounts []account.Account, acct account.Account, detail transaction.Detail, st editState, fragment string) {
	if r.Header.Get("HX-Request") != "true" {
		s.renderEditPage(w, r, cb, http.StatusOK, accounts, acct, detail, st, "")
		return
	}

	// Buffered for the same reason a page is: a template error must not arrive
	// as a 200 and half a form swapped into somebody's page.
	var buf bytes.Buffer
	page := s.buildEditPage(r, cb, accounts, acct, detail, st, "")
	if err := s.pages["edit-transaction.gohtml"].ExecuteTemplate(&buf, fragment, page); err != nil {
		s.log.Error("render fragment", "part", fragment, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Debug("write fragment", "path", r.URL.Path, "err", err)
	}
}

// renderEditPage writes the form, carrying whatever problem there is to report.
func (s *Server) renderEditPage(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, accounts []account.Account, acct account.Account, detail transaction.Detail, st editState, formError string) {
	s.render(w, r, status, "edit-transaction.gohtml", s.buildEditPage(r, cb, accounts, acct, detail, st, formError))
}

// buildEditPage assembles the page, tally and all.
func (s *Server) buildEditPage(r *http.Request, cb *checkbook, accounts []account.Account, acct account.Account, detail transaction.Detail, st editState, formError string) editPage {
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

	return editPage{
		layout:      s.pageLayout(r, cb, "Edit a transaction", accounts, acct.ID),
		Account:     acct,
		Currency:    string(acct.Currency),
		ID:          detail.ID,
		Form:        st.Form,
		FormError:   formError,
		Token:       st.Token,
		Split:       st.Split,
		Lines:       st.Lines,
		Tally:       tallyOf(st, acct.Currency),
		StatusLabel: statusLabel(detail.Status),
		Categories:  names,
	}
}

// tallyOf works out the three figures under the lines from what is typed.
//
// It is best-effort by design. The reader is part way through filling a form in,
// and a box that is not an amount yet is a reason to show a dash rather than to
// guess: the box says so itself when Save is pressed.
func tallyOf(st editState, cur money.Currency) tally {
	if !st.Split {
		return tally{}
	}

	text := st.Form.Payment
	if text == "" {
		text = st.Form.Deposit
	}
	amount, err := money.ParseDecimal(text, cur)
	if err != nil {
		return tally{Amount: unknownFigure, Assigned: unknownFigure, Unassigned: unknownFigure}
	}

	planned, problem := planSplits(st.Lines, amount)
	if problem != "" {
		return tally{Amount: formatAmount(amount), Assigned: unknownFigure, Unassigned: unknownFigure}
	}
	assigned, unassigned, err := reckon(planned, amount)
	if err != nil {
		return tally{Amount: formatAmount(amount), Assigned: unknownFigure, Unassigned: unknownFigure}
	}
	return tally{
		Amount:     formatAmount(amount),
		Assigned:   formatAmount(assigned),
		Unassigned: formatAmount(unassigned),
	}
}

// shownPlainly reports whether the one-box form can hold the whole of what the
// transaction records: no parts at all, or one part for the whole amount, under
// a name, with no memo of its own.
//
// Anything else opens in the editor, because the plain form could show it only
// by leaving something out -- and a box that cannot show a part is a box that
// would quietly remove it on the next Save.
func shownPlainly(d transaction.Detail) bool {
	switch len(d.Splits) {
	case 0:
		return true
	case 1:
		return d.Splits[0].Category != "" && d.Splits[0].Memo == "" &&
			d.Splits[0].Amount.Amount() == d.Amount.Amount()
	}
	return false
}

// linesOf is a stored transaction's parts as the editor shows them: unsigned,
// because the transaction says which way the money went.
func linesOf(splits []transaction.SplitDetail) []splitLine {
	lines := make([]splitLine, 0, len(splits))
	for _, s := range splits {
		lines = append(lines, splitLine{
			Category: s.Category,
			Memo:     s.Memo,
			Amount:   s.Amount.Abs().Decimal(),
		})
	}
	return lines
}

// seedLines is what Split this transaction opens with: the category and the
// amount already on the form, on the first line, and a blank line under it to
// divide into.
//
// It takes what is typed rather than what is stored, because the reader may have
// changed both before deciding to divide the result.
func seedLines(form entryForm, detail transaction.Detail) []splitLine {
	first := splitLine{Category: form.Category, Amount: form.Payment}
	if first.Amount == "" {
		first.Amount = form.Deposit
	}
	if len(detail.Splits) == 1 {
		first.Memo = detail.Splits[0].Memo
	}
	return []splitLine{first, {}}
}

// postedLines reads the lines back off the form.
//
// The three boxes of a line are three same-named fields, so the browser sends
// three lists in the order the rows appear and position is what pairs them up.
// The lengths are taken apart rather than assumed equal: a request this program
// did not draw the form for is answered, not trusted.
func postedLines(v url.Values) []splitLine {
	categories, memos, amounts := v["split_category"], v["split_memo"], v["split_amount"]
	n := max(len(categories), len(memos), len(amounts))

	lines := make([]splitLine, 0, n)
	for i := range n {
		lines = append(lines, splitLine{
			Category: at(categories, i),
			Memo:     at(memos, i),
			Amount:   at(amounts, i),
		})
	}
	return lines
}

// at is s[i] trimmed, or the empty string when there is no s[i].
func at(s []string, i int) string {
	if i >= len(s) {
		return ""
	}
	return strings.TrimSpace(s[i])
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
