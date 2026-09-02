// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
)

// restoredParam carries the path of a restored checkbook through the redirect
// that follows a copy, so the reader lands with the answer on screen and the box
// below it already holding the file they are about to open.
//
// swappedParam and keptParam do the same for the one-press restore, which lands
// on the register rather than on the open form: the first names the backup the
// records came from, the second the checkbook that was displaced. Both are base
// names, checked before they are put into a sentence.
const (
	restoredParam = "restored"
	swappedParam  = "restoredfrom"
	keptParam     = "kept"
)

// dateShown is how a backup's moment is written on these pages: the local
// calendar clock, which is what the stamp in the name already is.
//
// Nothing here is an instant crossing a timezone, so RG-5's rule that the
// browser does the converting is not in play: a backup taken at 14:15 is
// labelled 14:15 in the room the machine is in.
const dateShown = "2 Jan 2006, 15:04"

// RestoreRequest names a backup to restore and where to put the copy.
type RestoreRequest struct {
	// Source is the backup being restored from.
	Source string

	// Dest is the checkbook to write. It never names a file that already
	// exists: restoring is what somebody reaches for after something went
	// wrong, and the file they would be writing over is often the evidence.
	Dest string
}

// backupEntry is one row of the list.
type backupEntry struct {
	// Path is the file, absolute. It is what the form posts back, and it is
	// checked against a freshly made listing before anything acts on it.
	Path string

	// Name is what the reader sees: the path relative to the folder that was
	// searched, so a copy in backups/ is visibly in backups/.
	Name string

	// Taken is the moment, formatted, and FromName says where it came from --
	// the stamp in the name, or the file's own modification time. The page says
	// which, because they answer different questions when a file has been moved.
	Taken    string
	FromName bool

	// Size is the file's size, rounded for reading.
	Size string

	// IsBackup distinguishes a backup from a checkbook that could still be
	// restored from.
	IsBackup bool
}

// restoreOffer is the list of files that could be restored from, and what can be
// done with them.
type restoreOffer struct {
	// Folder is where this program looked: the checkbook's own folder and the
	// backups folder beside it.
	Folder string

	// Checkbook is the file a restore would replace. Empty when there is none
	// this program could put a file back at.
	Checkbook string

	Backups []backupEntry

	// CanRestore says the one-press restore is available. Withheld says why it
	// is not, and one of the two is always set: an action that cannot be carried
	// out is not offered, and a reader who expected it deserves the reason.
	CanRestore bool
	Withheld   string

	// Error is a folder that could not be read. It is different from finding
	// nothing, and a page that showed an empty list for it would be saying the
	// household has no backups.
	Error string

	// Generation goes back with the restore so a tab older than the checkbook
	// now open is refused rather than applied to a database it was never looking
	// at (CO-3).
	Generation uint64

	ReturnTo string
}

// restorePage is GET /checkbook/restore.
type restorePage struct {
	layout
	Restore *restoreOffer

	// Message is why a press was refused, shown above the list. The reader is
	// left on the page that offers the next thing to try (RG-4) rather than on
	// an error page they have to navigate back from.
	Message string
}

// restoreConfirmPage asks before replacing (RG-3).
type restoreConfirmPage struct {
	layout

	Chosen    backupEntry
	Checkbook string

	// Generation is carried through the confirmation to the press, the same way
	// the close form carries it.
	Generation uint64
	ReturnTo   string
}

// handleRestorePage lists the backups (BK-4).
//
// It is deliberately not wrapped in withCheckbook. That answers 503 when nothing
// is open, and nothing-open -- a checkbook closed, or one that would not open at
// all -- is the case this page exists for.
func (s *Server) handleRestorePage(w http.ResponseWriter, r *http.Request) {
	cb := s.currentCheckbook()
	s.render(w, r, http.StatusOK, "restore.gohtml", restorePage{
		layout:  s.pageLayout(r, cb, "Restore a backup", s.accountList(r, cb), 0),
		Restore: s.restoreOffer(r),
	})
}

// handleConfirmRestore asks before replacing (RG-3).
//
// Restoring is not destructive -- the checkbook that is there is kept, under a
// name the page gives -- but it does replace every record in front of the
// household with the ones in a file from another day, in every tab they have
// open. That is not something one press should do without saying so.
func (s *Server) handleConfirmRestore(w http.ResponseWriter, r *http.Request) {
	cb := s.currentCheckbook()
	offer := s.restoreOffer(r)

	chosen, ok := offer.find(r.URL.Query().Get("path"))
	switch {
	case !offer.CanRestore:
		s.restorePageWith(w, r, cb, http.StatusConflict, offer, offer.Withheld)
		return
	case !ok:
		s.restorePageWith(w, r, cb, http.StatusUnprocessableEntity, offer,
			"That file is not one of the backups listed below, so nothing was done. Choose one from the list.")
		return
	}

	s.render(w, r, http.StatusOK, "restore-confirm.gohtml", restoreConfirmPage{
		layout:     s.pageLayout(r, cb, "Restore a backup", s.accountList(r, cb), 0),
		Chosen:     chosen,
		Checkbook:  offer.Checkbook,
		Generation: offer.Generation,
		ReturnTo:   offer.ReturnTo,
	})
}

// handleRestoreSwap restores a backup and puts it in place, in one press.
//
// The order is restore first and close second, which is not the obvious one.
// backup.Restore reads its source on a connection of its own and writes only
// inside the destination's folder, so it never touches the checkbook and does
// not need it closed. Doing the long, failure-prone step while the register is
// still open and serving buys three things: a bad backup, a schema from a newer
// release, a full disk or an unwritable folder is refused with the checkbook
// still open, where there is no recovery path to write and none to test; the
// real work doubles as a write-probe on the folder; and the window in which
// every tab says 503 shrinks to a close, two renames and an open.
func (s *Server) handleRestoreSwap(w http.ResponseWriter, r *http.Request) {
	cb := s.currentCheckbook()

	if err := r.ParseForm(); err != nil {
		s.restorePageWith(w, r, cb, http.StatusBadRequest, s.restoreOffer(r),
			"The browser sent a form this program could not decode, so nothing was restored. Choose a backup from the list and press again.")
		return
	}
	gen, _ := strconv.ParseUint(r.PostForm.Get("generation"), 10, 64)

	offer := s.restoreOffer(r)
	chosen, ok := offer.find(strings.TrimSpace(r.PostForm.Get("path")))
	switch {
	case !offer.CanRestore:
		s.restorePageWith(w, r, cb, http.StatusConflict, offer, offer.Withheld)
		return
	case !ok:
		s.restorePageWith(w, r, cb, http.StatusUnprocessableEntity, offer,
			"That file is not one of the backups listed below, so nothing was done. Choose one from the list.")
		return
	case chosen.Path == offer.Checkbook:
		s.restorePageWith(w, r, cb, http.StatusUnprocessableEntity, offer,
			"That is the checkbook itself, not a copy of it. Nothing was done.")
		return
	case !s.generationMatches(gen):
		s.staleRestore(w, r, offer, "")
		return
	}

	// Step 2, and it runs outside ctl on purpose: a VACUUM of the household's
	// whole database must not block Close or Quit while it runs.
	dir := filepath.Dir(offer.Checkbook)
	working, err := backup.RestoredName(dir, time.Now())
	if err != nil {
		s.log.Error("name the restored checkbook", "dir", dir, "err", err)
		s.restorePageWith(w, r, cb, http.StatusConflict, offer,
			"A name for the restored copy could not be found in "+dir+". Nothing was done, your checkbook is still open, and nothing about it has changed.")
		return
	}
	res, err := backup.Restore(r.Context(), chosen.Path, working)
	if err != nil {
		s.log.Error("restore a backup", "source", chosen.Path, "dest", working, "err", err)
		s.restorePageWith(w, r, cb, http.StatusUnprocessableEntity, offer, swapRefused(chosen, working, err))
		return
	}
	s.log.Info("backup restored", "source", chosen.Path, "path", res.Path, "bytes", res.Bytes)

	s.putRestoredInPlace(w, r, offer, chosen, res.Path, gen)
}

// putRestoredInPlace is steps 3 to 6: close, move aside, move in, open.
//
// ctl is held across all four as one critical section. Between the two renames
// there is no file at the checkbook's name, and a POST /checkbook/open for that
// name in that window would have storage.Open create an empty checkbook, migrate
// it, and adopt it -- and then the second rename would put a file over it,
// leaving a live pool on an unlinked inode and a household typing into nothing.
// ctl is never taken by a request that reads the register, so this blocks only
// other control actions.
func (s *Server) putRestoredInPlace(w http.ResponseWriter, r *http.Request, offer *restoreOffer, chosen backupEntry, restored string, gen uint64) {
	s.ctl.Lock()
	defer s.ctl.Unlock()

	// Checked again under the lock. The first check was cheap and early, so that
	// a stale tab does not spend a minute copying a database before being told;
	// this one is the one that decides.
	if !s.generationMatches(gen) {
		s.staleRestore(w, r, offer, restored)
		return
	}

	// Synchronously, unlike Close: the file cannot be renamed until the pool
	// behind it has let go, and on Windows the rename is what would fail.
	if cb, ok := s.retire(gen); ok {
		if err := s.closeRetired(cb); err != nil {
			s.log.Error("close the checkbook", "database", cb.path, "err", err)
		}
	}

	kept, err := backup.Replace(restored, offer.Checkbook)
	if err != nil {
		s.log.Error("put the restored checkbook in place",
			"restored", restored, "checkbook", offer.Checkbook, "err", err)
		s.swapFailed(w, r, offer, restored, kept, err)
		return
	}
	s.log.Info("checkbook replaced", "checkbook", offer.Checkbook, "kept", kept, "from", chosen.Path)

	// Detached from the request. A reader who closes the tab mid-swap must not
	// cancel the reopen and leave the program with nothing open.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()

	store, err := s.open(ctx, OpenRequest{Path: offer.Checkbook})
	if err != nil {
		// The swap already succeeded, so there is nothing to roll back: the
		// records the reader asked for are at the checkbook's name. What failed
		// is opening them, and that is the ordinary no-checkbook page's subject.
		s.log.Error("open the restored checkbook", "path", offer.Checkbook, "err", err)
		p := DescribeOpenError(err, offer.Checkbook)
		s.renderRestoredButNotOpen(w, r, p, offer.Checkbook, kept)
		return
	}
	cb, previous := s.adopt(store)
	s.log.Info("checkbook opened", "database", cb.path)
	if previous != nil {
		go func() {
			if err := s.closeRetired(previous); err != nil {
				s.log.Error("close the checkbook", "database", previous.path, "err", err)
			}
		}()
	}

	// See Other, so the reload that follows is a GET of the register rather than
	// a second restore.
	target := "/?" + swappedParam + "=" + url.QueryEscape(chosen.Name)
	if kept != "" {
		target += "&" + keptParam + "=" + url.QueryEscape(filepath.Base(kept))
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// swapRefused is the wording for a restore that failed before anything moved.
//
// Every branch ends the same way, because that is the reader's real question at
// this moment: their checkbook is still open and nothing about it has changed.
func swapRefused(chosen backupEntry, working string, err error) string {
	const nothingChanged = " Your checkbook is still open and nothing about it has changed."

	switch {
	case errors.Is(err, backup.ErrMissingDirectory):
		return "The folder " + filepath.Dir(working) + " is no longer there, so the copy could not be written." + nothingChanged

	case errors.Is(err, storage.ErrMissingFile):
		return "There is no longer a file at " + chosen.Path + ". It may have been moved or deleted since this page was drawn." + nothingChanged

	case errors.Is(err, backup.ErrNotBackup):
		return chosen.Name + " is not a checkbook or a backup of one, so there is nothing in it to restore. It has not been altered." + nothingChanged

	case errors.Is(err, storage.ErrDatabaseTooNew):
		return chosen.Name + " was written by a newer version of the program than this one, so this one cannot bring a copy of it up to date. Use that version to restore it. The backup has not been altered." + nothingChanged

	case errors.Is(err, backup.ErrNotVerified):
		return "A copy of " + chosen.Name + " was made and then would not open as a checkbook, so it was removed rather than left looking like one. This usually means the backup itself is damaged. Try the backup before it." + nothingChanged

	case errors.Is(err, backup.ErrDestinationExists), errors.Is(err, backup.ErrNameInUse):
		return "A name for the restored copy could not be found in " + filepath.Dir(working) + "." + nothingChanged

	default:
		return "The backup could not be copied. Check that the disk is not full and that " + filepath.Dir(working) + " can be written to. The backup has not been altered." + nothingChanged
	}
}

// swapFailed answers a swap that got as far as moving files.
//
// backup.Replace reports which of the three outcomes it reached, and they need
// three different things from the reader, so they are answered separately rather
// than folded into "the restore failed".
func (s *Server) swapFailed(w http.ResponseWriter, r *http.Request, offer *restoreOffer, restored, kept string, err error) {
	switch {
	case errors.Is(err, backup.ErrCheckbookDisplaced):
		// The one unacceptable outcome, and the only one where the program
		// cannot decide for the household which file belongs at the name.
		s.renderNoCheckbookPage(w, r, http.StatusInternalServerError,
			"Your records are safe and both files are still on disk, but neither is at "+offer.Checkbook+
				" and only you can say which should be. The copy restored from the backup is "+restored+
				", and the checkbook you had is beside it under a checkbook-replaced- name. Rename the one you want to "+
				filepath.Base(offer.Checkbook)+" in your file manager, then open it below. Nothing was deleted.",
			nil, OpenRequest{Path: restored}, RestoreRequest{}, "")

	case errors.Is(err, backup.ErrNotPutInPlace):
		s.renderNoCheckbookPage(w, r, http.StatusConflict,
			"The copy restored from the backup could not take the checkbook's name, so your checkbook was put back exactly as it was. Nothing was changed and nothing was deleted. The copy is still at "+
				restored+", if you would rather open that instead.",
			nil, OpenRequest{Path: offer.Checkbook}, RestoreRequest{}, "")

	default:
		// ErrCheckbookNotMoved and everything else: nothing moved at all.
		s.renderNoCheckbookPage(w, r, http.StatusConflict,
			"Your checkbook could not be moved aside, so nothing was replaced. It is exactly where it was, with everything in it. This usually means another program has the file open — close it and try again. The copy restored from the backup is at "+
				restored+", and you can open that instead.",
			nil, OpenRequest{Path: offer.Checkbook}, RestoreRequest{}, "")
	}
	_ = kept
}

// renderRestoredButNotOpen is step 6 having failed: the swap succeeded and the
// file at the checkbook's name will not open. Both files are named, because the
// reader's way back is the one that was kept.
func (s *Server) renderRestoredButNotOpen(w http.ResponseWriter, r *http.Request, p Problem, checkbook, kept string) {
	message := "The backup was restored and is now at " + checkbook + ", but it would not open. Nothing was deleted."
	if kept != "" {
		message += " The checkbook you had is kept as " + kept + ", and you can open that to get back to where you were."
	}
	s.renderNoCheckbookPage(w, r, http.StatusServiceUnavailable, message, &p,
		OpenRequest{Path: checkbook}, RestoreRequest{}, "")
}

// staleRestore refuses a press made on a page drawn for a different checkbook
// (CO-3), the same way handleClose refuses a stale close.
//
// restored, when it is set, is a copy that was already written and is not thrown
// away: it cost a full copy of the database to make, and it is a perfectly good
// checkbook.
func (s *Server) staleRestore(w http.ResponseWriter, r *http.Request, offer *restoreOffer, restored string) {
	cb := s.currentCheckbook()
	message := "This page was drawn for a different checkbook from the one that is open now, so nothing was replaced. Nothing was changed."
	if restored != "" {
		message += " A copy restored from the backup was already written, and it is at " + restored + "; it has been left there rather than thrown away, and you can open it."
	}
	message += " Reload this page and press again if you still want to restore."
	s.restorePageWith(w, r, cb, http.StatusConflict, offer, message)
}

// restorePageWith redraws the list with a refusal above it, so the reader is
// left on the page that offers the next thing to try (RG-4).
func (s *Server) restorePageWith(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, offer *restoreOffer, message string) {
	if cb == nil && offer.Checkbook == "" {
		s.renderNoCheckbook(w, r, status, message)
		return
	}
	s.render(w, r, status, "restore.gohtml", restorePage{
		layout:  s.pageLayout(r, cb, "Restore a backup", s.accountList(r, cb), 0),
		Restore: offer,
		Message: message,
	})
}

// restoreOffer builds the list and decides whether the one-press restore can be
// offered at all.
func (s *Server) restoreOffer(r *http.Request) *restoreOffer {
	offer := &restoreOffer{
		Checkbook: s.checkbookPath(),
		ReturnTo:  returnTo(r),
	}
	cb := s.currentCheckbook()
	if cb != nil {
		offer.Generation = cb.gen
	}

	switch {
	case s.open == nil:
		offer.Withheld = "This build cannot open a checkbook by itself, so a backup can be copied to a new file but not put in place. Use the form below, then start the program on the copy."
	case cb != nil && cb.inMemory:
		offer.Withheld = "The sample household is open. It is held in memory and there is no file behind it to replace. Use the form below to restore a backup to a file of its own, then open that."
	case cb != nil && cb.readOnly:
		offer.Withheld = "This checkbook is open for reading only, so it is not a file this program will replace — and if it is a backup, replacing it is the one thing that would stop it being the copy you took (BK-6). Close it first, or use the form below to restore to a new file."
	case offer.Checkbook == "":
		offer.Withheld = "This program has not been told which file your checkbook is, so there is nothing for a restored copy to replace. Use the form below to restore to a file you name, then open it."
	default:
		offer.CanRestore = true
	}

	dir := ""
	if offer.Checkbook != "" {
		dir = filepath.Dir(offer.Checkbook)
	} else if cb != nil && !cb.inMemory {
		dir = filepath.Dir(cb.path)
	} else if closed, inMemory, _ := s.closedCheckbook(); closed != "" && !inMemory {
		dir = filepath.Dir(closed)
	}
	if dir == "" {
		return offer
	}
	offer.Folder = dir

	found, err := backup.Find(dir)
	if err != nil {
		s.log.Error("list backups", "dir", dir, "err", err)
		offer.Error = "The folder " + dir + " could not be read, so this is not a list of the backups you have — it is no list at all. Check the disk the checkbook is on."
		return offer
	}
	for _, b := range found {
		if b.Path == offer.Checkbook {
			// The checkbook itself is not a copy of itself.
			continue
		}
		offer.Backups = append(offer.Backups, entryFor(b, dir))
	}
	return offer
}

// entryFor formats one file for the list.
//
// Dates and sizes are made here rather than in a template: a template that
// formats is a template that can be given a different value to format.
func entryFor(b backup.Backup, dir string) backupEntry {
	e := backupEntry{
		Path:     b.Path,
		Name:     displayName(b.Path, dir),
		Size:     formatBytes(b.Bytes),
		IsBackup: b.IsBackup,
	}
	if stamp, ok := backup.StampInName(b.Path); ok {
		e.Taken, e.FromName = stamp.Format(dateShown), true
	} else {
		e.Taken = b.Taken.Local().Format(dateShown)
	}
	return e
}

// displayName is the path relative to the folder searched, so a copy inside
// backups/ is visibly inside it. Anything the relative path cannot express falls
// back to the whole path, which is never wrong, only long.
func displayName(path, dir string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

// find looks a posted path up in the listing this offer was built from.
//
// The value arrives in a form, and a restore acts on the household's whole file:
// nothing is done to a path this program did not just list for itself. The
// listing is remade on the request that acts, not carried over from the one that
// drew the page.
func (o *restoreOffer) find(path string) (backupEntry, bool) {
	if path == "" {
		return backupEntry{}, false
	}
	for _, e := range o.Backups {
		if e.Path == path {
			return e, true
		}
	}
	return backupEntry{}, false
}

// formatBytes rounds a file size to something a person reads.
func formatBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value, exp := float64(n)/unit, 0
	for value >= unit && exp < 3 {
		value /= unit
		exp++
	}
	return fmt.Sprintf("%.1f %sB", value, [...]string{"k", "M", "G", "T"}[exp])
}

// handleRestoreCopy restores a backup to a new file, replacing nothing (BK-4).
//
// It is a control route and takes no lease. It does not need one: backup.Restore
// works from paths, on connections it opens itself, so the checkbook this
// program has open -- if any -- is neither read nor touched. What it produces is
// a third file, which the reader then opens in the ordinary way.
//
// This is the older of the two restores and it stays, unchanged, beside the one
// that swaps. It needs no Opener, which is what keeps the offer to restore
// honest in a build that cannot open a checkbook at all; and restoring to a name
// of your own is the answer when the file you want back is not the one you are
// working in.
func (s *Server) handleRestoreCopy(w http.ResponseWriter, r *http.Request) {
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

// restoreFailed answers a copy-restore that did not happen.
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
		msg = "There is no file at " + req.Source + ". Check the path against your file manager: your backups are named checkbook-YYYYMMDD-HHMMSS.db and sit in the backups folder beside your checkbook. Nothing was written."

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
// no-checkbook page, which is where the copy form is offered, and the ordinary
// error page when a checkbook is open and the request came from somewhere else.
func (s *Server) restoreRefused(w http.ResponseWriter, r *http.Request, cb *checkbook, req RestoreRequest, message string) {
	if cb == nil {
		s.renderRestoreFailed(w, r, http.StatusUnprocessableEntity, message, req)
		return
	}
	s.fail(w, r, cb, http.StatusUnprocessableEntity, s.accountList(r, cb),
		"That backup was not restored", message,
		"Restore a backup lists the copies this program can find, and restoring from that page replaces this checkbook in one press. To restore to a file of its own instead, close this checkbook first.")
}

// suggestName offers a name beside the one that was taken, so the reader has
// something to type rather than a rule to satisfy.
func suggestName(dest string) string {
	ext := filepath.Ext(dest)
	return strings.TrimSuffix(dest, ext) + "-restored" + ext
}
