// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"net/http"
)

// RestartHint is how to start the program again.
//
// It is composed by the command and only printed here. The command is the one
// thing that knows how it was started -- which executable, from which directory,
// with which flags actually given -- and it captures that at startup, before
// anything can change directory.
type RestartHint struct {
	// Command is the line to type. Empty when the command did not supply one, in
	// which case the page says nothing rather than guessing.
	Command string

	// Directory is where to type it.
	Directory string
}

// quitPage is the confirmation, and goodbyePage is what is left on screen.
type quitPage struct {
	Version string

	// Database is what is open, so the reader can see what they are closing
	// along with the program. Empty when nothing is open.
	Database string
}

type goodbyePage struct {
	Version string
	Restart RestartHint
}

// handleConfirmQuit asks before ending the program (RG-3).
//
// It takes no lease -- the quit that follows must not wait for itself -- and it
// stands alone rather than using the layout, because it has to look the same
// whether or not a checkbook is open.
func (s *Server) handleConfirmQuit(w http.ResponseWriter, r *http.Request) {
	page := quitPage{Version: s.version}
	if cb := s.currentCheckbook(); cb != nil {
		page.Database = cb.path
	}
	s.renderStandalone(w, r, http.StatusOK, quitFile, page)
}

// handleQuit ends the program.
//
// The order here is the whole of it: render into a buffer, write it, flush it,
// and only then cancel. quit() is the last statement in the function, never
// earlier, because the browser is about to lose the connection and whatever has
// not reached it will not arrive.
//
// The page carries its styles inline for the same reason. /static/app.css is a
// second request, and by the time the browser makes it there is nothing left to
// answer it.
//
// Nothing is closed here. Cancelling the context is what unblocks main, which
// shuts the server down -- draining the handlers, including this one -- and then
// closes the checkbook with the lease count already at zero.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !s.renderStandalone(w, r, http.StatusOK, goodbyeFile, goodbyePage{
		Version: s.version,
		Restart: s.restart,
	}) {
		// The reader has an error page instead of a goodbye. Stopping the
		// program on top of that would leave them with no way to ask why.
		return
	}
	http.NewResponseController(w).Flush()

	s.log.Info("quitting at the reader's request")
	s.quit()
}
