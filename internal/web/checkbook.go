// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
)

// closeGrace is how long a close waits for the requests already using the
// checkbook before closing it anyway.
//
// Closing anyway is safe. Pool.Close does not merely wait: it shuts the door on
// new borrows, cancels each borrowed connection's context -- firing
// sqlite3_interrupt -- and only then drains, so it forces the borrowers to fail
// and hand their connections back rather than hoping they will. An interrupted
// write rolls back cleanly, because sqlitex restores the interrupt channel
// before running its ROLLBACK, and a lease holder that reaches for a connection
// afterwards gets "pool closed" as an error, not a panic.
//
// The grace exists because a request has no bounded lifetime -- there is no
// WriteTimeout, so a browser that stops reading a response holds its handler
// goroutine indefinitely -- and "close the checkbook" must not become "the
// application is dead".
const closeGrace = 5 * time.Second

// checkbook is one open database together with the requests using it.
//
// path and inMemory are copied out of the Store when it is adopted, so a page
// can name the database (BK-3) without reaching into a pool that may be closing.
type checkbook struct {
	store    *storage.Store
	path     string
	inMemory bool
	readOnly bool

	// isBackup says the file is one of this program's backups rather than a
	// checkbook that happens to be open read-only. It is what lets the frame
	// say which of the two the reader is looking at, and what lets the page
	// after closing offer to restore it rather than to back it up again.
	isBackup bool

	// gen distinguishes this checkbook from any other opened in this run, so a
	// button pressed in a tab that has been on screen since before a swap is
	// refused rather than applied to a database it was never looking at. It is
	// the same shape as the updated_at token transaction.SetStatus already uses
	// for a row (CO-3).
	gen uint64

	// users counts the requests holding this checkbook open.
	//
	// Add is only ever called under Server.mu.RLock while Server.current still
	// points here, and current is cleared under Server.mu.Lock. So no Add can
	// follow the Wait in closeRetired -- which is the one thing sync.WaitGroup
	// forbids, and the reason the lock cannot be dropped for an atomic pointer.
	users sync.WaitGroup
}

// acquire takes a lease on the current checkbook.
//
// The lock is held just long enough to read a pointer and count a user. It is
// deliberately not held for the request: an RWMutex held across a handler would
// deadlock the moment the close handler went through the same accessor -- Go's
// RWMutex is not reentrant and has no upgrade -- and one stalled request would
// block the writer, which would block every reader after it.
//
// The second return value is always safe to call, so a caller can defer it
// before deciding what to do about a nil checkbook.
func (s *Server) acquire() (*checkbook, func()) {
	s.mu.RLock()
	cb := s.current
	if cb != nil {
		cb.users.Add(1)
	}
	s.mu.RUnlock()

	if cb == nil {
		return nil, func() {}
	}
	return cb, cb.users.Done
}

// adopt makes store the current checkbook and returns it, along with whatever it
// replaced. The caller closes the replaced one; adopt closes nothing.
func (s *Server) adopt(store *storage.Store) (*checkbook, *checkbook) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gen++
	cb := &checkbook{
		store:    store,
		path:     store.Path(),
		inMemory: store.IsMemory(),
		readOnly: store.ReadOnly(),
		isBackup: store.IsBackup(),
		gen:      s.gen,
	}
	previous := s.current
	s.current = cb

	if !cb.readOnly && !cb.inMemory {
		// The checkbook a restore would put a file back at. A backup opened
		// read-only is deliberately not it: restoring over the copy somebody is
		// reading is the mistake BK-6 exists to stop.
		s.writablePath = cb.path
	}
	// Whatever would not open at startup is no longer what is wrong.
	s.startupProblem = nil
	return cb, previous
}

// checkbookPath is the file this program calls "the checkbook".
//
// It is the one that is open when that one could be written to, and otherwise
// the last one that could, falling back to what -db named -- which is the answer
// in the case this whole feature exists for, where -db never opened at all.
// A backup being read and the sample household are neither, so neither can be
// the file a restore replaces.
func (s *Server) checkbookPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if cb := s.current; cb != nil && !cb.readOnly && !cb.inMemory {
		return cb.path
	}
	return s.writablePath
}

// generationMatches reports whether gen names the checkbook that is open now.
//
// Zero means "nothing is open", which is a state a restore is started from as
// readily as any other, so it is a value to match rather than a wildcard. That
// is the difference between this and retire's gen argument, where zero means
// "whatever is there" because Close on shutdown has nothing to compare.
func (s *Server) generationMatches(gen uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.current == nil {
		return gen == 0
	}
	return s.current.gen == gen
}

// startupFailure reports why the checkbook named at startup would not open, or
// nil once something has opened.
func (s *Server) startupFailure() *Problem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startupProblem
}

// retire removes the current checkbook and returns it.
//
// gen is the generation the caller believes is current. A mismatch means the
// checkbook changed in another window since the page carrying the button was
// drawn, and the request is refused rather than applied to a database it was
// never looking at (CO-3).
//
// retire closes nothing: closing is closeRetired's job, outside every lock.
func (s *Server) retire(gen uint64) (*checkbook, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cb := s.current
	if cb == nil || (gen != 0 && cb.gen != gen) {
		return nil, false
	}
	s.current = nil
	s.closedPath = cb.path
	s.closedInMemory = cb.inMemory
	s.closedIsBackup = cb.isBackup
	return cb, true
}

// closeRetired waits for the requests still using cb, then closes it.
//
// No lock is held here, and that is the point: a slow request delays only this,
// never another request, never a later Open, never Quit. From the instant retire
// cleared the pointer no new lease can be taken, so the set of users only
// shrinks.
//
// The wait carries a deadline because a request's lifetime has no bound. See
// closeGrace for why closing anyway is safe.
func (s *Server) closeRetired(cb *checkbook) error {
	done := make(chan struct{})
	go func() {
		cb.users.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(closeGrace):
		// Close interrupts the connections it is waiting on rather than hoping
		// for them, so this is a delay being reported, not a leak.
		s.log.Warn("closing the checkbook with requests still in flight",
			"database", cb.path, "waited", closeGrace)
	}
	return cb.store.Close()
}

// current reports the checkbook a page should describe, without taking a lease.
// It is for naming the database, never for reaching into it.
func (s *Server) currentCheckbook() *checkbook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// closedUnderneath reports whether cb stopped being the current checkbook while
// the caller was holding a lease on it.
//
// It is the difference between two messages. A pool that reports "pool closed"
// in the middle of listing accounts would otherwise be rendered as "the database
// reported an error while listing accounts", which is true of nothing the reader
// did and useless as advice. What actually happened is that the checkbook was
// closed in another window, and there is a page that says exactly that.
func (s *Server) closedUnderneath(cb *checkbook) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current == nil || s.current.gen != cb.gen
}

// closedCheckbook reports the database that was closed most recently, so the
// page the reader lands on can name it and offer the right thing to do with it:
// back it up, open it again, or -- if it was a backup being read -- restore it.
func (s *Server) closedCheckbook() (path string, inMemory, isBackup bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closedPath, s.closedInMemory, s.closedIsBackup
}

// dbFailed answers a database error hit while holding a lease on cb.
//
// If the checkbook was closed underneath the request, "pool closed" is what the
// database reported and it is not what happened. The reader gets the page that
// says the checkbook was closed, which is both true and actionable. Otherwise
// this is an ordinary failure and heading, detail and nextStep are used as they
// were written.
func (s *Server) dbFailed(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, accounts []account.Account, heading, detail, nextStep string) {
	if s.closedUnderneath(cb) {
		s.renderNoCheckbook(w, r, http.StatusServiceUnavailable, "")
		return
	}
	s.fail(w, r, cb, status, accounts, heading, detail, nextStep)
}

// checkbookHandler is a handler that needs an open checkbook.
//
// It cannot be registered on the mux directly: the only way to turn one into an
// http.HandlerFunc is withCheckbook. A (store, ok) helper would leave a
// forgotten call as a nil dereference inside the pool -- a blank 500 and a stack
// trace nobody reads -- and this makes the same mistake a compile error.
type checkbookHandler func(http.ResponseWriter, *http.Request, *checkbook)

// withWritableCheckbook is withCheckbook for a route that writes.
//
// A read-only checkbook is a backup being looked at, and every write action is
// withheld from the pages rather than offered and then refused. This is the
// other half of that: a request that arrives anyway -- a tab left open from
// before, a typed address, a bookmarked form -- gets a written explanation
// rather than SQLite's "attempt to write a readonly database" (RG-4).
func (s *Server) withWritableCheckbook(h checkbookHandler) http.HandlerFunc {
	return s.withCheckbook(func(w http.ResponseWriter, r *http.Request, cb *checkbook) {
		if cb.readOnly {
			detail := cb.path + " was opened read-only, which is how a backup is looked at without being changed. Nothing was written."
			next := "To make changes, close this and open the checkbook you keep your records in. To work from this file, close it and restore it to a new name."
			if cb.isBackup {
				detail = cb.path + " is a backup. It can be read and it can be restored, but it is never written to: the moment it were, it would stop being the copy that was taken. Nothing was written."
				next = "To make changes, close this and open the checkbook you keep your records in. To work from these records, close this and restore the backup — that copies it to a new file and brings the copy up to date, leaving the backup as it is."
			}
			s.fail(w, r, cb, http.StatusConflict, s.accountList(r, cb),
				"This checkbook is open for reading only", detail, next)
			return
		}
		h(w, r, cb)
	})
}

// withCheckbook leases the current checkbook for the length of one request, or
// answers the no-checkbook page if there is none.
func (s *Server) withCheckbook(h checkbookHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cb, release := s.acquire()
		if cb == nil {
			// 503 rather than 404: the address is right, and nothing here is the
			// reader's request being wrong. It is the same reading NewProblem
			// takes of a database that would not open.
			s.renderNoCheckbook(w, r, http.StatusServiceUnavailable, "")
			return
		}
		defer release()
		h(w, r, cb)
	}
}
