// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
)

// restoredParam carries the path of a restored checkbook through the redirect
// that follows, so the reader lands with the answer on screen and the box below
// it already holding the file they are about to open.
const restoredParam = "restored"

// RestoreRequest names a backup to restore and where to put the copy.
type RestoreRequest struct {
	// Source is the backup being restored from.
	Source string

	// Dest is the checkbook to write. It never names a file that already
	// exists: restoring is what somebody reaches for after something went
	// wrong, and the file they would be writing over is often the evidence.
	Dest string
}

// handleRestore restores a backup to a new file (BK-4).
//
// It is a control route and takes no lease. It does not need one: backup.Restore
// works from paths, on connections it opens itself, so the checkbook this
// program has open -- if any -- is neither read nor touched. What it produces is
// a third file, which the reader then opens in the ordinary way.
//
// Restoring and opening stay two verbs on purpose. Restoring is what makes the
// records in a backup usable again; opening is what puts a checkbook in front of
// you. Rolling them together would mean the one press that copies a file also
// swaps out the register in every tab the household has open.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	cb := s.currentCheckbook()

	if err := r.ParseForm(); err != nil {
		s.restoreRefused(w, r, cb, RestoreRequest{},
			"The browser sent a form this program could not decode, so nothing was restored. Type the two paths again and press Restore.")
		return
	}

	req := RestoreRequest{
		Source: strings.TrimSpace(r.PostForm.Get("source")),
		Dest:   strings.TrimSpace(r.PostForm.Get("dest")),
	}
	back := "/checkbook"
	if v := r.PostForm.Get("return"); v != "" {
		back = safeReturn(v)
	}

	switch {
	case req.Source == "":
		s.restoreRefused(w, r, cb, req,
			"Type the path of the backup to restore from. Nothing was written.")
		return
	case req.Dest == "":
		s.restoreRefused(w, r, cb, req,
			"Type the path to restore to. It must be a file that does not exist yet: restoring never writes over one that does. Nothing was written.")
		return
	}

	// Made absolute here for the reason browserOpener does it: the program may
	// have been started from anywhere, and a relative path typed into a browser
	// box is relative to a working directory the reader cannot see.
	source, dest, err := absPair(req.Source, req.Dest)
	if err != nil {
		s.restoreRefused(w, r, cb, req,
			"That path could not be read as a path, so nothing was restored. Check it and press Restore again.")
		return
	}
	req.Source, req.Dest = source, dest

	res, err := backup.Restore(r.Context(), req.Source, req.Dest)
	if err != nil {
		s.log.Error("restore a backup", "source", req.Source, "dest", req.Dest, "err", err)
		s.restoreFailed(w, r, cb, req, err)
		return
	}
	s.log.Info("backup restored", "source", req.Source, "path", res.Path, "bytes", res.Bytes)

	// See Other, so the reload that follows is a GET and does not restore a
	// second time -- which would fail anyway, the destination now existing, but
	// would fail as an error page rather than as nothing happening.
	http.Redirect(w, r, back+"?"+restoredParam+"="+url.QueryEscape(res.Path), http.StatusSeeOther)
}

// absPair makes both paths absolute, or reports the first that could not be.
func absPair(a, b string) (string, string, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return "", "", err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return "", "", err
	}
	return absA, absB, nil
}

// restoreFailed answers a restore that did not happen.
//
// Every branch says the same thing: the backup was not altered, and nothing was
// left behind under the name the reader typed. A household that has just been
// told a restore failed needs to know which of their files they still have.
func (s *Server) restoreFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, req RestoreRequest, err error) {
	var msg string
	switch {
	case errors.Is(err, backup.ErrDestinationExists):
		msg = "There is already a file at " + req.Dest + ", and restoring never writes over one. " +
			"Choose a name that does not exist yet — " + suggestName(req.Dest) + ", say — and move it into place yourself once you have looked at it. Nothing was written."

	case errors.Is(err, backup.ErrMissingDirectory):
		msg = "The folder " + filepath.Dir(req.Dest) + " does not exist. The program writes the file but never the folder, so a mistyped path is reported rather than built. Create the folder yourself, or restore to a path inside one that is already there. Nothing was written."

	case errors.Is(err, storage.ErrMissingFile):
		msg = "There is no file at " + req.Source + ". Check the path against your file manager: your backups are named checkbook-YYYYMMDD-HHMMSS.db and sit beside your checkbook. Nothing was written."

	case errors.Is(err, backup.ErrNotBackup):
		msg = req.Source + " is not a checkbook or a backup of one, so there is nothing in it to restore. It has not been altered, and nothing was written."

	case errors.Is(err, storage.ErrDatabaseTooNew):
		msg = req.Source + " was written by a newer version of the program than this one, so this one cannot bring a copy of it up to date. Use that version to restore it. The backup has not been altered, and nothing was left behind."

	case errors.Is(err, backup.ErrNotVerified):
		// The copy was made and would not come up as a checkbook. That is a fact
		// about the backup, not about the copying, and it is worth saying plainly.
		msg = "A copy of " + req.Source + " was made and then would not open as a checkbook, so it was removed rather than left looking like one. This usually means the backup itself is damaged. " +
			"The backup is exactly as it was found, and nothing was written at " + req.Dest + ". Try the backup before it."

	default:
		msg = "The backup could not be restored. Check that the disk is not full and that " + filepath.Dir(req.Dest) + " can be written to. The backup has not been altered, and nothing was left behind."
	}
	s.restoreRefused(w, r, cb, req, msg)
}

// restoreRefused puts the message on whichever page the reader has: the
// no-checkbook page, which is where the form is offered, and the ordinary error
// page when a checkbook is open and the request came from somewhere else.
func (s *Server) restoreRefused(w http.ResponseWriter, r *http.Request, cb *checkbook, req RestoreRequest, message string) {
	if cb == nil {
		s.renderRestoreFailed(w, r, http.StatusUnprocessableEntity, message, req)
		return
	}
	s.fail(w, r, cb, http.StatusUnprocessableEntity, s.accountList(r, cb),
		"That backup was not restored", message,
		"Close this checkbook, then restore the backup from the page you land on. Your records are untouched either way.")
}

// suggestName offers a name beside the one that was taken, so the reader has
// something to type rather than a rule to satisfy.
func suggestName(dest string) string {
	ext := filepath.Ext(dest)
	return strings.TrimSuffix(dest, ext) + "-restored" + ext
}
