// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"net/http"
	"strconv"
	"strings"
)

// noCheckbookPage is what the reader sees when no database is open: because
// they closed one, or because the program was started without one.
//
// It stands alone rather than being built on the layout, for the same reason
// problem.gohtml does. The layout frames a page with the account list and the
// database in use, and here there is neither; an empty sidebar reading "None
// yet." would suggest an empty checkbook, which is a very different thing.
type noCheckbookPage struct {
	Version string

	// Closed is the database that was closed, and Copyable says whether it is a
	// file that can be copied. Naming it and saying it is now safe to copy is
	// the payoff that connects Close to backup: the program is no longer holding
	// the file open, so there is no -wal beside it to fold back in first.
	Closed   string
	Copyable bool

	// CanOpen and CanQuit say whether the program was built with those actions
	// wired up. An action that is not offered cannot be pressed and then refused.
	CanOpen bool
	CanQuit bool

	// Problem describes an attempt to open a checkbook that failed. It is
	// DescribeOpenError's answer, so a database from a newer release, a file
	// that is not a checkbook, and a missing folder each get the heading and the
	// ordered steps they already have elsewhere (RG-4).
	Problem *Problem

	// Message is a plainer failure than a Problem: a backup that could not be
	// written, say. Empty most of the time.
	Message string

	// Notice reports something that went right: a backup written.
	Notice string

	// Path is what was last typed into the open box, and ReadOnly whether the
	// box beside it was ticked, so a refused attempt comes back with the
	// reader's work still in it.
	Path     string
	ReadOnly bool
}

// handleCheckbook shows the checkbook page.
//
// With one open there is nothing to say here, so the reader is sent to their
// register: the register is the primary screen (RG-1), and this address is
// somewhere a bookmark or the browser's history can land.
func (s *Server) handleCheckbook(w http.ResponseWriter, r *http.Request) {
	if s.currentCheckbook() != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	s.renderNoCheckbook(w, r, http.StatusOK, "")
}

// renderNoCheckbook writes the page, with message shown above the form when
// something was refused.
func (s *Server) renderNoCheckbook(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.renderNoCheckbookPage(w, r, status, message, nil, OpenRequest{})
}

// renderOpenFailed writes the page carrying DescribeOpenError's account of why a
// checkbook would not open, and the path that was tried.
func (s *Server) renderOpenFailed(w http.ResponseWriter, r *http.Request, status int, p Problem, req OpenRequest) {
	s.renderNoCheckbookPage(w, r, status, "", &p, req)
}

func (s *Server) renderNoCheckbookPage(w http.ResponseWriter, r *http.Request, status int, message string, problem *Problem, req OpenRequest) {
	closed, inMemory := s.closedCheckbook()

	page := noCheckbookPage{
		Version:  s.version,
		Closed:   closed,
		Copyable: closed != "" && !inMemory,
		CanOpen:  s.open != nil,
		CanQuit:  s.quit != nil,
		Problem:  problem,
		Message:  message,
		Notice:   noticeFor(r),
		Path:     req.Path,
		ReadOnly: req.ReadOnly,
	}
	if page.Path == "" && !inMemory {
		// The obvious thing to open is the one just closed, so the box opens on
		// it. It is the shape of "close, copy the file, open it again", which is
		// the ritual this page exists to make possible without a terminal.
		page.Path = closed
	}

	s.renderStandalone(w, r, status, noCheckbookFile, page)
}

// handleConfirmClose asks before closing (RG-3).
//
// Closing destroys nothing, but it does take the register away from every tab
// the household has open, so it is not something a mistyped keystroke should do.
// The confirmation carries the generation of the checkbook it was drawn for.
func (s *Server) handleConfirmClose(w http.ResponseWriter, r *http.Request, cb *checkbook) {
	accounts := s.accountList(r, cb)
	s.render(w, r, http.StatusOK, "close-checkbook.gohtml", struct {
		layout
		Closing string
	}{
		layout:  s.pageLayout(r, cb, "Close checkbook", accounts, 0),
		Closing: cb.path,
	})
}

// handleClose closes the current checkbook.
//
// It takes no lease. A closer that waited for itself would never finish, and
// that rule is what the whole design turns on: the wait is coupled to connection
// lifetime, which Close can force to an end, never to request lifetime, which
// has no bound.
//
// The response is written before the store is closed, because the reader's real
// question -- can anything still reach my records -- was answered by retire, the
// moment the pointer was cleared.
func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, s.currentCheckbook(), http.StatusBadRequest, nil,
			"That request could not be read",
			"The browser sent a form this program could not decode, so nothing was closed.",
			"Go back and press Close checkbook again.")
		return
	}
	gen, _ := strconv.ParseUint(r.PostForm.Get("generation"), 10, 64)

	s.ctl.Lock()
	cb, ok := s.retire(gen)
	s.ctl.Unlock()

	if !ok {
		// Either two tabs pressed Close and the other got there first, which is
		// a success and answered as one, or the tab is older than the checkbook
		// now open and is refused (CO-3).
		if current := s.currentCheckbook(); current != nil {
			s.fail(w, r, current, http.StatusConflict, s.accountList(r, current),
				"The checkbook changed in another window",
				"This page was drawn for a different checkbook from the one that is open now, so it was not closed. Nothing was changed.",
				"Reload this page. If you still want to close what is open, press Close checkbook again from the reloaded page.")
			return
		}
		s.renderNoCheckbook(w, r, http.StatusOK, "")
		return
	}

	s.log.Info("checkbook closed", "database", cb.path)
	s.renderNoCheckbook(w, r, http.StatusOK, "")

	// Off the request goroutine, so a slow request delays only this. Close is
	// called exactly once: retire handed this checkbook to exactly one caller.
	go func() {
		if err := s.closeRetired(cb); err != nil {
			s.log.Error("close the checkbook", "database", cb.path, "err", err)
		}
	}()
}

// handleOpen opens a checkbook and makes it the current one.
//
// The opener is injected (see Opener), so this knows nothing about flags or
// about seeding sample data.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNoCheckbook(w, r, http.StatusBadRequest,
			"The browser sent a form this program could not decode, so nothing was opened. Type the path again and press Open.")
		return
	}

	req := OpenRequest{
		Path:     strings.TrimSpace(r.PostForm.Get("path")),
		Demo:     r.PostForm.Get("demo") != "",
		ReadOnly: r.PostForm.Get("readonly") != "",
	}
	if !req.Demo && req.Path == "" {
		s.renderNoCheckbook(w, r, http.StatusUnprocessableEntity,
			"Type the path of the checkbook to open, then press Open. A file that does not exist yet is created; a folder that does not exist is not.")
		return
	}

	s.ctl.Lock()
	defer s.ctl.Unlock()

	// A checkbook held in memory is closed before anything else is opened,
	// rather than after. It keeps nothing, so there is nothing to protect by
	// holding it open -- and it has to go first, because a shared in-memory
	// database survives as long as one connection is open: opening the demo
	// while the demo is open would find the sample household already there and
	// collide on a duplicate account name.
	if cb := s.currentCheckbook(); cb != nil && cb.inMemory {
		if retired, ok := s.retire(cb.gen); ok {
			if err := s.closeRetired(retired); err != nil {
				s.log.Error("close the checkbook", "database", retired.path, "err", err)
			}
		}
	}

	store, err := s.open(r.Context(), req)
	if err != nil {
		// Nothing is adopted, so current stays exactly as it was -- and where it
		// was nil it stays nil. It must never hold a Store whose pool is closed;
		// storage.Open closes its own pool on every failure path, so nothing
		// leaks by leaving it alone.
		s.log.Error("open the checkbook", "path", req.Path, "demo", req.Demo, "err", err)
		s.openFailed(w, r, req, err)
		return
	}

	// Adopt first, then retire what it replaced, so a checkbook the reader is
	// reading is never taken away for one that turned out not to open.
	cb, previous := s.adopt(store)
	s.log.Info("checkbook opened", "database", cb.path)
	if previous != nil {
		// No retire: adopt already moved current off it, so no new lease can be
		// taken on it and the set of users only shrinks. Off the request
		// goroutine, so a slow reader of the old register delays only this.
		go func() {
			if err := s.closeRetired(previous); err != nil {
				s.log.Error("close the checkbook", "database", previous.path, "err", err)
			}
		}()
	}

	// See Other, so the reload that follows is a GET of the register rather than
	// a second open.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// openFailed answers a checkbook that would not open.
//
// DescribeOpenError is reused rather than NewProblem. NewProblem builds a
// separate handler with a catch-all mux of its own, and swapping the running
// server's Handler would be a data race; even dispatched internally, its "/"
// route would swallow the two addresses the reader has left. What deserves reuse
// is the mapping from sentinel to heading and ordered steps, and that is what
// this takes.
func (s *Server) openFailed(w http.ResponseWriter, r *http.Request, req OpenRequest, err error) {
	name := req.Path
	if req.Demo {
		name = "the sample household"
	}
	s.renderOpenFailed(w, r, http.StatusUnprocessableEntity, DescribeOpenError(err, name), req)
}
