// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/backup"
)

// backedUpParam carries the name of a backup just written through the redirect
// that follows it, so the reader lands back where they were with the answer on
// screen.
//
// A name rather than a sentence, and the sentence is composed in Go from it, so
// nothing puts words on the page that the program did not write. The name is
// checked against the shape backup.Create produces before it is used at all.
const backedUpParam = "backedup"

// noticeFor reads the notice a redirect is carrying, if any.
//
// It is called from pageLayout, so any page can carry one and no page has to
// remember to.
func noticeFor(r *http.Request) string {
	name := r.URL.Query().Get(backedUpParam)
	if name == "" || !validBackupName(name) {
		return ""
	}
	return "A backup was written beside your checkbook, as " + name +
		". It was reopened and read back as a checkbook before being named, so it is a copy you can restore from. Copy it somewhere else -- another disk, or a service you already use -- while you are thinking of it."
}

// validBackupName reports whether name is one this program wrote: the exact
// shape backup.Create gives a copy, and nothing else.
//
// The name arrives in a URL, which anything can compose, and it is put into a
// sentence the reader is meant to believe. Templates would escape it either way;
// this is about not saying something untrue.
func validBackupName(name string) bool {
	rest, ok := strings.CutPrefix(name, "checkbook-")
	if !ok {
		return false
	}
	rest, ok = strings.CutSuffix(rest, ".db")
	if !ok {
		return false
	}
	// 20260902-141530, and 20260902-141530-2 for the second backup to land in
	// the same second.
	parts := strings.Split(rest, "-")
	if len(parts) != 2 && len(parts) != 3 {
		return false
	}
	if len(parts[0]) != 8 || !allDigits(parts[0]) {
		return false
	}
	if len(parts[1]) != 6 || !allDigits(parts[1]) {
		return false
	}
	return len(parts) == 2 || allDigits(parts[2])
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// handleBackup writes a verified copy of the database beside it (BK-2, BK-5).
//
// It is a control route: it acts on the file rather than on a record, so it is
// wrapped by control and reached only by POST from a page this program served.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, nil,
			"That request could not be read",
			"The browser sent a form this program could not decode, so no backup was written.",
			"Go back to the register and press Back up now again.")
		return
	}
	back := returnTo(r.PostForm.Get("return"))

	path := s.store.Path()
	if s.store.IsMemory() {
		// -demo has nothing on disk to copy. Writing sample data into the
		// household's folder under a backup's name would be worse than refusing.
		s.fail(w, r, http.StatusConflict, s.accountList(r),
			"There is nothing here to back up",
			"This register is the sample household, held in memory. It is discarded when the program stops, and there is no file behind it to copy.",
			"Start the program on your own checkbook to back it up. Nothing was written.")
		return
	}

	res, err := backup.Create(r.Context(), path, filepath.Dir(path))
	if err != nil {
		s.backupFailed(w, r, path, err)
		return
	}
	s.log.Info("backup written", "path", res.Path, "bytes", res.Bytes)

	// See Other, so the reload that follows is a GET and does not take a second
	// backup. The reader lands back on the page they pressed the link from.
	http.Redirect(w, r, back+"?"+backedUpParam+"="+filepath.Base(res.Path), http.StatusSeeOther)
}

// backupFailed answers a backup that did not happen.
//
// Every branch says the same thing first: nothing was written under a backup's
// name. A household that has just been told a backup failed needs to know
// whether there is now a file they might mistake for one.
func (s *Server) backupFailed(w http.ResponseWriter, r *http.Request, path string, err error) {
	accounts := s.accountList(r)
	switch {
	case errors.Is(err, backup.ErrMissingDirectory):
		s.fail(w, r, http.StatusConflict, accounts,
			"The folder holding your checkbook is gone",
			"The backup goes beside the database, and "+filepath.Dir(path)+" no longer exists. No backup was written.",
			"Check the disk the checkbook is on, then press Back up now again.")

	case errors.Is(err, backup.ErrNotVerified):
		// BK-5: a file was written and it would not reopen. That is worth
		// saying plainly -- it is a fact about the records, not about the copy.
		s.log.Error("backup could not be verified", "database", path, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"The copy could not be read back",
			"A copy of "+path+" was made and then would not reopen as a checkbook, so it was removed rather than left looking like a backup. This usually means the database itself is damaged.",
			"Restore your most recent good backup and open that copy. Keep the current file: it is what shows what went wrong.")

	default:
		s.log.Error("backup", "database", path, "err", err)
		s.fail(w, r, http.StatusInternalServerError, accounts,
			"The backup could not be written",
			"The program could not write a copy of "+path+". Nothing was written under a backup's name.",
			"Check that the disk is not full and that the folder can be written to, then press Back up now again.")
	}
}

// returnTo picks the page a control route sends the reader back to.
//
// Only a path on this server is accepted. The value comes from a form, and a
// redirect that would take somebody off their own machine is not something this
// program should ever emit.
func returnTo(v string) string {
	if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") {
		return "/"
	}
	// A query string of its own would be lost under the one the redirect adds,
	// and a fragment is the browser's business.
	if i := strings.IndexAny(v, "?#"); i >= 0 {
		v = v[:i]
	}
	return v
}

// accountList reads the accounts for a page that has already failed at something
// else. A second failure here is not worth a second error page: the page simply
// offers no list.
func (s *Server) accountList(r *http.Request) []account.Account {
	accounts, err := account.List(r.Context(), s.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		return nil
	}
	return accounts
}
