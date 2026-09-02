// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
)

// backedUpParam carries the name of a backup just written through the redirect
// that follows it, so the reader lands back where they were with the answer on
// screen.
//
// A name rather than a sentence, and the sentence is composed in Go from it, so
// nothing puts words on the page that the program did not write. The name is
// checked against the shape backup.Create produces before it is used at all.
const backedUpParam = "backedup"

// newFolderParam says the backups folder was created to hold the copy. ST-10
// lets the program make a folder it named itself, in a folder that already holds
// the household's records, in answer to an explicit action -- and requires it to
// say that it did so. This is how it says.
const newFolderParam = "newfolder"

// noticeFor reads the notice a redirect is carrying, if any.
//
// It is called from pageLayout, so any page can carry one and no page has to
// remember to.
func (s *Server) noticeFor(r *http.Request) string {
	q := r.URL.Query()

	if name := q.Get(backedUpParam); name != "" {
		if !backup.ValidBackupName(name) {
			return ""
		}
		notice := "A backup was written to the backups folder beside your checkbook, as " + backup.FolderName + "/" + name + "."
		if q.Get(newFolderParam) != "" {
			notice += " The " + backup.FolderName + " folder did not exist, so it was made for it."
		}
		return notice +
			" It was reopened and read back as a checkbook before being named, so it is a copy you can restore from. Copy it somewhere else — another disk, or a service you already use — while you are thinking of it."
	}

	if name := q.Get(keptParam); name != "" {
		// The one-press restore. Both files are named, because the reader's way
		// back is the one that was kept, and a page that only said "restored"
		// would leave them wondering what happened to what they had.
		if !backup.ValidReplacedName(name) {
			return ""
		}
		notice := "Your checkbook was restored"
		if from := q.Get(swappedParam); s.restorableBeside(from) {
			notice += " from " + from
		}
		return notice + ". The checkbook you had is kept as " + name +
			", in the same folder, and nothing was deleted — open it if you want to be back where you were. Back up now, so the trail continues from here."
	}

	if path := q.Get(restoredParam); path != "" {
		if !restoredCheckbookExists(path) {
			return ""
		}
		return "That backup was restored to " + path +
			", as an ordinary checkbook. It was opened and read back before it was given that name, and the backup itself was not altered. Open it below to check the balances, then back it up so the trail continues from here."
	}

	return ""
}

// restoredCheckbookExists reports whether path really is a checkbook this
// program can open, right now.
//
// The value arrives in a URL, which anything can compose, and it goes into a
// sentence the reader is meant to believe. validBackupName answers the same
// question for a backup's name by its shape; a restored checkbook is named by
// the reader and has no shape to check, so the file is asked instead. Reading
// four bytes of a header is cheap and it is the difference between reporting
// something and asserting it.
func restoredCheckbookExists(path string) bool {
	appID, err := storage.ApplicationID(path)
	return err == nil && appID == storage.AppID
}

// restorableBeside reports whether name really is a file beside the checkbook
// that this program could have restored from, right now.
//
// The value arrives in a URL, which anything can compose, and it goes into a
// sentence the reader is meant to believe. A backup's own name can be checked by
// its shape, but this one is whatever the household called the file they chose,
// so the file is asked instead -- the same trick restoredCheckbookExists uses,
// and for the same reason.
func (s *Server) restorableBeside(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.Contains(name, "..") {
		return false
	}
	checkbook := s.checkbookPath()
	if checkbook == "" {
		return false
	}
	appID, err := storage.ApplicationID(filepath.Join(filepath.Dir(checkbook), filepath.FromSlash(name)))
	return err == nil && (appID == storage.AppID || appID == storage.BackupAppID)
}

// handleBackup writes a verified copy of the database beside it (BK-2, BK-5).
//
// It is a control route and takes no lease. It does not need one: backup.Create
// works from a path on a connection it opens itself, which is what lets the same
// action copy the checkbook that is open and the one that has just been closed
// -- the offer on the page the reader lands on after closing, and the point of
// being able to close one at all.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	cb := s.currentCheckbook()

	if err := r.ParseForm(); err != nil {
		s.fail(w, r, cb, http.StatusBadRequest, s.accountList(r, cb),
			"That request could not be read",
			"The browser sent a form this program could not decode, so no backup was written.",
			"Go back and press Back up now again.")
		return
	}
	back := safeReturn(r.PostForm.Get("return"))

	path, inMemory := "", false
	if cb != nil {
		path, inMemory = cb.path, cb.inMemory
	} else {
		path, inMemory, _ = s.closedCheckbook()
	}

	switch {
	case path == "":
		s.renderNoCheckbook(w, r, http.StatusConflict,
			"There is no checkbook to back up. This program has not had one open since it started.")
		return
	case inMemory:
		// -demo has nothing on disk to copy. Writing sample data into the
		// household's folder under a backup's name would be worse than refusing.
		s.backupRefused(w, r, cb, http.StatusConflict,
			"There is nothing here to back up",
			"That register is the sample household, held in memory. It is discarded when the program stops, and there is no file behind it to copy.",
			"Start the program on your own checkbook to back it up. Nothing was written.")
		return
	}

	// Into the backups folder beside the checkbook, which is made if it is not
	// there. That is the one directory this program creates, and ST-10 is what
	// licenses it: a name the program chose, inside a folder that already holds
	// the household's records, in answer to a press -- and said out loud on the
	// page that follows. backup.Create learns none of this; the convention lives
	// in backup.Folder, so the terminal register gets it by calling the same
	// function rather than by remembering a string.
	dir := backup.Folder(path)
	newFolder, err := ensureFolder(dir)
	if err != nil {
		s.log.Error("make the backups folder", "dir", dir, "err", err)
		s.backupRefused(w, r, cb, http.StatusInternalServerError,
			"The backups folder could not be made",
			"Backups go in "+dir+", and that folder is not there and could not be created. No backup was written.",
			"Check that the disk is not full and that "+filepath.Dir(path)+" can be written to, then press Back up now again.")
		return
	}

	res, err := backup.Create(r.Context(), path, dir)
	if err != nil {
		s.backupFailed(w, r, cb, path, err)
		return
	}
	s.log.Info("backup written", "path", res.Path, "bytes", res.Bytes, "new folder", newFolder)

	// See Other, so the reload that follows is a GET and does not take a second
	// backup. The reader lands back on the page they pressed the link from.
	target := back + "?" + backedUpParam + "=" + filepath.Base(res.Path)
	if newFolder {
		target += "&" + newFolderParam + "=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// ensureFolder makes dir if it is not there, and reports whether it had to.
//
// Only the one level: MkdirAll would build a whole path, and the only folder
// this program may create is the one it named itself inside a folder that is
// already there (ST-10). A missing parent is a checkbook whose folder has gone,
// which is a different failure and gets a different message.
func ensureFolder(dir string) (created bool, err error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil && info.IsDir():
		return false, nil
	case err == nil:
		return false, fmt.Errorf("%s is not a folder", dir)
	case !errors.Is(err, os.ErrNotExist):
		return false, err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

// backupRefused answers a backup that did not happen, on whichever page the
// reader has: the ordinary error page when a checkbook is open, and the
// no-checkbook page when there is none to frame one with.
func (s *Server) backupRefused(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, heading, detail, nextStep string) {
	if cb == nil {
		s.renderNoCheckbook(w, r, status, detail+" "+nextStep)
		return
	}
	s.fail(w, r, cb, status, s.accountList(r, cb), heading, detail, nextStep)
}

// backupFailed answers a backup that could not be written.
//
// Every branch says the same thing first: nothing was written under a backup's
// name. A household that has just been told a backup failed needs to know
// whether there is now a file they might mistake for one.
func (s *Server) backupFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, path string, err error) {
	switch {
	case errors.Is(err, backup.ErrMissingDirectory):
		s.backupRefused(w, r, cb, http.StatusConflict,
			"The folder holding your checkbook is gone",
			"The backup goes beside the database, and "+filepath.Dir(path)+" no longer exists. No backup was written.",
			"Check the disk the checkbook is on, then press Back up now again.")

	case errors.Is(err, backup.ErrNotVerified):
		// BK-5: a file was written and it would not reopen. That is worth
		// saying plainly -- it is a fact about the records, not about the copy.
		s.log.Error("backup could not be verified", "database", path, "err", err)
		s.backupRefused(w, r, cb, http.StatusInternalServerError,
			"The copy could not be read back",
			"A copy of "+path+" was made and then would not reopen as a checkbook, so it was removed rather than left looking like a backup. This usually means the database itself is damaged.",
			"Restore your most recent good backup and open that copy. Keep the current file: it is what shows what went wrong.")

	default:
		s.log.Error("backup", "database", path, "err", err)
		s.backupRefused(w, r, cb, http.StatusInternalServerError,
			"The backup could not be written",
			"The program could not write a copy of "+path+". Nothing was written under a backup's name.",
			"Check that the disk is not full and that the folder can be written to, then press Back up now again.")
	}
}

// returnTo is the address to put in a control form, so pressing Back up now
// leaves the reader on the page they pressed it from.
//
// Only a GET's address is offered. An error page rendered in answer to a POST is
// at an address that answers nothing else, and sending somebody back to it would
// turn a successful backup into a page that says there is nothing there.
func returnTo(r *http.Request) string {
	if r.Method != http.MethodGet {
		return "/"
	}
	return safeReturn(r.URL.Path)
}

// safeReturn reads the address a control form posted back.
//
// Only a path on this server is accepted. The value comes from a form, and a
// redirect that would take somebody off their own machine is not something this
// program should ever emit.
func safeReturn(v string) string {
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
func (s *Server) accountList(r *http.Request, cb *checkbook) []account.Account {
	if cb == nil {
		return nil
	}
	accounts, err := account.List(r.Context(), cb.store)
	if err != nil {
		s.log.Error("list accounts", "err", err)
		return nil
	}
	return accounts
}
