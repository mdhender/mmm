// Copyright (c) 2026 Michael D Henderson.

// Package web serves the local check register over loopback.
//
// It is a thin layer: it parses a request, asks a domain package for records,
// formats them, and writes HTML. No balance is computed here and no SQL is
// written here, so the terminal register shows the same numbers from the same
// code (SPECIFICATION.md TS-2, TS-3).
//
// There is no authentication, session, or CSRF machinery, and there must not be
// (PL-7). The server binds to loopback, has no remote origin, and has no notion
// of accounts, so there is no second principal for such a mechanism to tell
// apart.
package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// OpenRequest names a checkbook to open.
type OpenRequest struct {
	// Path is the database file. It is ignored when Demo is set.
	Path string

	// ReadOnly asks for a database that cannot be written to.
	//
	// A backup is only ever opened this way -- storage.Open refuses one outright
	// (BK-6) -- so for a backup this box is not a precaution the reader has to
	// remember, it is the only way in. It stays offered rather than being
	// inferred because it is also the safe way to look at an ordinary checkbook,
	// and because a reader who ticks it has said what they mean.
	ReadOnly bool

	// Demo asks for the sample household, held in memory.
	Demo bool
}

// Opener opens a checkbook on behalf of a request.
//
// It is injected rather than written here because this package must not learn
// about flags or about seeding sample data: seedDemo exists only in the command,
// and openStore owns the wording ST-6 asks for when a directory is missing. A
// nil Opener means the program cannot open a checkbook from the browser, and the
// route is not registered at all.
//
// Passing a request's context to an Opener is safe: storage.Open uses it for one
// pool.Get, and Put clears the interrupt, so no finished request's cancellation
// is left wired into a long-lived pool.
type Opener func(ctx context.Context, req OpenRequest) (*storage.Store, error)

// Options is what a Server is built from.
type Options struct {
	// Store is the checkbook to start with. It may be nil, in which case the
	// program starts with nothing open and says so.
	Store *storage.Store

	// Open opens another one. Nil withholds the action rather than offering it
	// and then failing.
	Open Opener

	// Quit ends the program. Nil withholds it, for the same reason.
	Quit func()

	// Restart is how to start the program again, shown on the page the reader
	// is left with after quitting. It is composed by the command, which is the
	// only thing that knows how it was started, and printed here.
	Restart RestartHint

	// CheckbookPath is the file the program was started on, absolute.
	//
	// It is what "the checkbook" means when there is none open -- because it
	// would not open, or because it has been closed -- and it is what a restore
	// puts a file back at. The command makes it absolute before handing it over:
	// a relative path is relative to a working directory the reader cannot see.
	CheckbookPath string

	// Problem describes a checkbook that would not open at startup.
	//
	// The program keeps running and serves the ordinary server rather than a
	// page at every address, because the reader whose checkbook is corrupt is
	// exactly the reader who needs the restore list -- and a mux that answers
	// every address with one static page has no address to offer it at.
	Problem *Problem

	// Version is shown in the footer, so a bug report can say which build
	// produced a page.
	Version string

	Log *slog.Logger
}

// Server serves the register. It is safe for concurrent use: the household may
// well have several tabs open on the same account.
//
// The checkbook it serves is not fixed. It can be closed from the browser and
// another opened without restarting the program, which is what makes a backup
// something a household can take, verify and restore without finding a terminal.
// See checkbook.go for how a request holds one open.
type Server struct {
	// mu guards current and the generation counter, and is held only long enough
	// to read a pointer or swap one. It is never held across a request.
	mu      sync.RWMutex
	current *checkbook
	gen     uint64

	// closedPath is the database that was closed most recently. It outlives the
	// checkbook so the page the reader lands on can name the file and say it is
	// now safe to copy -- which is the whole point of being able to close one.
	closedPath     string
	closedInMemory bool
	closedIsBackup bool

	// writablePath is the checkbook this program would put a restored file
	// back at: the current one when it is a file that could be written to, and
	// otherwise the last one that was, falling back to what -db named.
	//
	// It is not closedPath. That is set by retire for any checkbook, a backup
	// opened read-only included, and restoring over the backup somebody was
	// reading is precisely the thing BK-6 exists to stop.
	writablePath string

	// startupProblem is why the checkbook named by -db would not open. It is
	// shown on the no-checkbook page until something opens successfully, at
	// which point it stops being what is wrong.
	startupProblem *Problem

	// ctl serializes Open against Close. It is never taken by a request that
	// reads the register, so opening a large database -- migrations and all --
	// blocks another control action and nothing else.
	ctl sync.Mutex

	open    Opener
	quit    func()
	restart RestartHint
	version string
	log     *slog.Logger

	mux   *http.ServeMux
	pages map[string]*template.Template

	// standalone holds the pages that are not built on the layout, each parsed
	// on its own, so they cannot live in pages.
	standalone map[string]*template.Template
}

// New builds a server.
//
// Templates are parsed once, here, so a broken template fails at startup rather
// than in the middle of rendering somebody's register.
func New(opts Options) (*Server, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}

	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	standalone := make(map[string]*template.Template, len(standaloneFiles))
	for _, name := range standaloneFiles {
		// The partials come along, because a standalone page uses the same
		// restore list the layout pages do and there is one copy of it.
		t, err := template.New(name).ParseFS(templateFS, append([]string{"templates/" + name}, partialPaths()...)...)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", name, err)
		}
		standalone[name] = t
	}

	s := &Server{
		writablePath:   opts.CheckbookPath,
		startupProblem: opts.Problem,

		open:    opts.Open,
		quit:    opts.Quit,
		restart: opts.Restart,
		version: opts.Version,
		log:     log,
		mux:     http.NewServeMux(),
		pages:   pages,

		standalone: standalone,
	}
	if opts.Store != nil {
		s.adopt(opts.Store)
	}
	s.routes()
	return s, nil
}

// Close closes whatever checkbook is open, waiting for the requests using it.
//
// It is what the command defers in place of store.Close: the store the program
// started with may not be the one it ends with.
func (s *Server) Close() error {
	cb, ok := s.retire(0)
	if !ok {
		return nil
	}
	return s.closeRetired(cb)
}

// routes registers the handlers.
//
// The patterns name their method, so anything but GET on a register URL gets a
// 405 from the mux rather than a handler that has to check.
//
// Everything that reads or writes records goes through withCheckbook, which
// leases the open checkbook for the length of the request. The routes that are
// deliberately not wrapped are the ones that have to work when there is no
// checkbook to lease, or that must not wait for one: /checkbook and its two
// actions, /backup (which takes a path, and can copy the file just closed),
// /quit, and the static files.
//
// Middleware around the mux would be the wrong tool for the same job: it would
// need a second, string-matching copy of the routing table, living away from
// here and free to drift.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.withCheckbook(s.handleRoot))
	// More specific than /accounts/{id}, so ServeMux prefers it and an account
	// can never be numbered "new".
	// The four routes that write go through withWritableCheckbook, which refuses
	// a checkbook opened read-only. The new-account form is one of them: a form
	// that can only be refused is not an offer worth making.
	s.mux.HandleFunc("GET /accounts/new", s.withWritableCheckbook(s.handleNewAccount))
	s.mux.HandleFunc("POST /accounts", s.withWritableCheckbook(s.handleCreateAccount))
	s.mux.HandleFunc("GET /accounts/{id}", s.withCheckbook(s.handleRegister))
	// The entry has its own address rather than a POST to the register's, so an
	// unmatched method on a register URL still gets a 405 from the mux.
	s.mux.HandleFunc("POST /accounts/{id}/transactions", s.withWritableCheckbook(s.handleCreateTransaction))
	s.mux.HandleFunc("POST /accounts/{id}/transactions/{txn}/status", s.withWritableCheckbook(s.handleSetStatus))
	// Changing and removing a transaction (RG-2). Both are asked for on a page
	// of their own and answered with a redirect: an edit moves the running
	// balance of every row below it and a removal moves the totals besides, so
	// unlike the cleared mark there is no fragment to swap.
	s.mux.HandleFunc("GET /accounts/{id}/transactions/{txn}/edit", s.withWritableCheckbook(s.handleEditForm))
	s.mux.HandleFunc("POST /accounts/{id}/transactions/{txn}", s.withWritableCheckbook(s.handleUpdateTransaction))
	// Removing is destructive, so it is confirmed first (RG-3).
	s.mux.HandleFunc("GET /accounts/{id}/transactions/{txn}/delete", s.withWritableCheckbook(s.handleConfirmDelete))
	s.mux.HandleFunc("POST /accounts/{id}/transactions/{txn}/delete", s.withWritableCheckbook(s.handleDeleteTransaction))

	// The control routes. They act on the file or on the program rather than on
	// a record, so they are POST only and go through the same-origin check in
	// control.go. None of them takes a lease: a closer that waited for itself
	// would never finish.
	s.mux.HandleFunc("GET /checkbook", s.handleCheckbook)
	s.mux.HandleFunc("GET /checkbook/close", s.withCheckbook(s.handleConfirmClose))
	s.mux.HandleFunc("POST /checkbook/close", s.control(s.handleClose))
	s.mux.HandleFunc("POST /backup", s.control(s.handleBackup))
	if s.open != nil {
		s.mux.HandleFunc("POST /checkbook/open", s.control(s.handleOpen))
	}
	// Restoring is four routes, and the split is what keeps every offer on the
	// page honest.
	//
	// The list and the confirmation are GETs and are always registered: nothing
	// open -- including a checkbook that would not open -- is the case the
	// feature exists for, so neither may be wrapped in withCheckbook, which
	// answers 503 in exactly that case.
	//
	// The one press that replaces the checkbook has to reopen afterwards, so it
	// needs an Opener and is withheld without one. The older restore-to-a-copy
	// does not: it writes a third file and swaps nothing, which is what lets the
	// rule that restoring is always offered survive.
	s.mux.HandleFunc("GET /checkbook/restore", s.handleRestorePage)
	s.mux.HandleFunc("GET /checkbook/restore/confirm", s.handleConfirmRestore)
	if s.open != nil {
		s.mux.HandleFunc("POST /checkbook/restore", s.control(s.handleRestoreSwap))
	}
	s.mux.HandleFunc("POST /checkbook/restore/copy", s.control(s.handleRestoreCopy))
	if s.quit != nil {
		s.mux.HandleFunc("GET /quit", s.handleConfirmQuit)
		s.mux.HandleFunc("POST /quit", s.control(s.handleQuit))
	}

	// The catch-all is last in precedence, not in registration: ServeMux picks
	// the most specific pattern. It exists so a mistyped address gets a page
	// that says what to do next rather than net/http's bare "404 page not
	// found" (SPECIFICATION.md RG-4). It is wrapped too, so a mistyped address
	// with nothing open gets the no-checkbook page rather than a 404 that
	// offers a list of accounts nobody can read.
	s.mux.HandleFunc("GET /", s.withCheckbook(s.handleNotFound))

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		// staticFS is embedded at compile time, so this cannot fail at run time.
		panic(fmt.Sprintf("web: static assets: %v", err))
	}
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// parsePages builds one template set per page: the shared layout plus that
// page's own "main". Parsing them into a single set is not possible because each
// page defines a template of the same name.
func parsePages() (map[string]*template.Template, error) {
	names, err := fs.Glob(templateFS, "templates/*.gohtml")
	if err != nil {
		return nil, fmt.Errorf("web: find templates: %w", err)
	}

	pages := make(map[string]*template.Template)
	for _, name := range names {
		base := name[len("templates/"):]
		// The layout is not a page, a partial is not a page, and several pages
		// are not built on the layout: see no-checkbook.gohtml for why those
		// stand alone.
		if base == layoutFile || isPartial(base) || isStandalone(base) {
			continue
		}
		files := append([]string{"templates/" + layoutFile, name}, partialPaths()...)
		t, err := template.New(layoutFile).ParseFS(templateFS, files...)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", base, err)
		}
		pages[base] = t
	}

	for _, required := range []string{"register.gohtml", "empty.gohtml", "error.gohtml", "new-account.gohtml", "close-checkbook.gohtml", "restore.gohtml", "restore-confirm.gohtml", "edit-transaction.gohtml", "delete-transaction.gohtml"} {
		if pages[required] == nil {
			return nil, fmt.Errorf("web: missing template %s", required)
		}
	}
	return pages, nil
}

const (
	layoutFile = "layout.gohtml"

	// noCheckbookFile is rendered when the checkbook has been closed. It stands
	// alone for the same reason problemFile does: the layout frames a page with
	// the account list and the database path, and there is neither.
	noCheckbookFile = "no-checkbook.gohtml"

	// quitFile asks before ending the program (RG-3), and goodbyeFile is what is
	// on screen afterwards. Both stand alone, and goodbyeFile carries its styles
	// inline: the stylesheet is a second request, and by the time the browser
	// makes it there is nothing left to answer.
	quitFile    = "quit.gohtml"
	goodbyeFile = "goodbye.gohtml"
)

// standaloneFiles are the pages not built on the layout. parsePages skips them,
// and New parses each on its own; a page left out of this list would be parsed
// against the layout, define no "main", and fail at render time -- which is
// exactly what parsing at startup exists to prevent.
var standaloneFiles = []string{noCheckbookFile, quitFile, goodbyeFile}

// isStandalone reports whether base is one of them.
func isStandalone(base string) bool {
	for _, name := range standaloneFiles {
		if base == name {
			return true
		}
	}
	return false
}

// partialFiles define named templates rather than a page, and are parsed into
// every page set -- layout pages and standalone pages alike.
//
// There is one of them, and it earns its place: the list of backups to restore
// from appears both on its own page and on the page the reader lands on when
// nothing is open, and two copies of that markup would drift.
var partialFiles = []string{"restore-list.gohtml"}

// isPartial reports whether base is one of them.
func isPartial(base string) bool {
	for _, name := range partialFiles {
		if base == name {
			return true
		}
	}
	return false
}

// partialPaths is partialFiles as ParseFS wants them.
func partialPaths() []string {
	paths := make([]string, 0, len(partialFiles))
	for _, name := range partialFiles {
		paths = append(paths, "templates/"+name)
	}
	return paths
}

// layout is the part of a page that does not depend on which page it is. Every
// page struct embeds it.
type layout struct {
	Title string

	// Database is the file the records are in. The UI is required to identify it
	// (SPECIFICATION.md BK-3): a household that cannot tell which file it is
	// editing cannot back the right one up.
	Database string

	Version string

	// Accounts fills the account list, and ActiveID marks the one being read.
	Accounts []account.Account
	ActiveID int64

	// Open says whether there is a checkbook behind this page. A control route
	// can render one when there is not.
	Open bool

	// Generation identifies the open checkbook, and goes back with Close so a
	// button pressed in a tab older than the current checkbook is refused
	// rather than applied to a database it was never looking at (CO-3).
	Generation uint64

	// ReturnTo is the address a control action sends the reader back to: the
	// page they were on when they pressed it. It is only ever a page they could
	// have arrived at by asking for it, so a form that failed does not offer to
	// send them back to an address that only answers a POST.
	ReturnTo string

	// Notice reports something that happened to the checkbook rather than to
	// the page in front of the reader: a backup written, a checkbook closed. It
	// is in the frame rather than on a page because the actions that raise one
	// are in the frame too, and every page can be the one the reader is on when
	// they press.
	Notice string

	// ReadOnly marks a database opened for reading only -- a backup being looked
	// at. Like Ephemeral it is a property of the database rather than of a flag,
	// and it is in the frame because every page has to say so: a register that
	// cannot be written to looks exactly like one that can until somebody tries.
	ReadOnly bool

	// IsBackup narrows ReadOnly to the case it was built for. "Read-only" says
	// what cannot be done here; "this is a backup" says what the file is, which
	// is what decides whether the reader's next move is to close it or to
	// restore it (BK-6).
	IsBackup bool

	// CanRestore says the one-press restore is available, so the sidebar offers
	// it. It is withheld for the sample household, for a backup being read, and
	// for a build with no Opener -- an action that could only be refused is not
	// an offer worth making.
	CanRestore bool

	// Ephemeral marks a database held in memory -- the sample household -demo
	// serves. Every page says so, because a register that keeps nothing looks
	// exactly like one that does, and somebody entering real transactions into
	// the demo would lose them without ever being told.
	Ephemeral bool
}

// pageLayout builds the frame every page shares. It is one function so that a
// new page cannot quietly omit the database it is editing (BK-3) or the mark
// that says the database is a temporary one.
// cb may be nil: a control route can render a page with nothing open, and
// "No checkbook open" is still an answer to the question BK-3 asks.
func (s *Server) pageLayout(r *http.Request, cb *checkbook, title string, accounts []account.Account, activeID int64) layout {
	l := layout{
		Title:    title,
		Database: "No checkbook open",
		Version:  s.version,
		Accounts: accounts,
		ActiveID: activeID,
		ReturnTo: returnTo(r),
		Notice:   s.noticeFor(r),
	}
	if cb != nil {
		l.Database = cb.path
		l.Ephemeral = cb.inMemory
		l.ReadOnly = cb.readOnly
		l.IsBackup = cb.isBackup
		l.Generation = cb.gen
		l.Open = true
	}
	l.CanRestore = s.open != nil && s.checkbookPath() != "" &&
		(cb == nil || (!cb.inMemory && !cb.readOnly))
	return l
}

// render writes a page, or an error page if the template fails.
//
// The page is rendered into a buffer first. Writing straight to the
// ResponseWriter would commit a 200 and half a document before a template error
// could be discovered, leaving the reader with a page that just stops.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	t := s.pages[page]
	if t == nil {
		s.log.Error("unknown template", "page", page, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, layoutFile, data); err != nil {
		s.log.Error("render page", "page", page, "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		// The reader navigated away or the connection dropped. Nothing to do,
		// but do not pretend the page arrived.
		s.log.Debug("write page", "path", r.URL.Path, "err", err)
	}
}

// renderStandalone writes one of the pages that is not built on the layout.
//
// Buffered like every other page: a template error must not arrive as a 200 and
// half a document. It matters more here than anywhere else, because one of these
// is the last thing the program writes before it stops.
func (s *Server) renderStandalone(w http.ResponseWriter, r *http.Request, status int, name string, data any) bool {
	t := s.standalone[name]
	if t == nil {
		s.log.Error("unknown template", "page", name, "path", r.URL.Path)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}

	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render page", "page", name, "path", r.URL.Path, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		s.log.Debug("write page", "path", r.URL.Path, "err", err)
		return false
	}
	return true
}

// errorPage carries a failure to the reader.
type errorPage struct {
	layout

	// Heading says what happened and Detail says why, in the reader's terms.
	Heading string
	Detail  string

	// NextStep is what the reader can safely do now. Required, not optional:
	// SPECIFICATION.md RG-4 says an error must say what happened *and* what to
	// do next, and an error page without it is only half an answer.
	NextStep string
}

// fail renders an error page. accounts may be nil when the failure was reading
// them, and cb may be nil when there is no checkbook open; the layout copes with
// both.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, cb *checkbook, status int, accounts []account.Account, heading, detail, nextStep string) {
	s.render(w, r, status, "error.gohtml", errorPage{
		layout:   s.pageLayout(r, cb, heading, accounts, 0),
		Heading:  heading,
		Detail:   detail,
		NextStep: nextStep,
	})
}
