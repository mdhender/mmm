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
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/storage"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server serves the register. It is safe for concurrent use: the household may
// well have several tabs open on the same account.
type Server struct {
	store   *storage.Store
	version string
	log     *slog.Logger

	mux   *http.ServeMux
	pages map[string]*template.Template
}

// New builds a server over store. version is shown in the footer so a bug report
// can say which build produced a page.
//
// Templates are parsed once, here, so a broken template fails at startup rather
// than in the middle of rendering somebody's register.
func New(store *storage.Store, version string, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}

	pages, err := parsePages()
	if err != nil {
		return nil, err
	}

	s := &Server{
		store:   store,
		version: version,
		log:     log,
		mux:     http.NewServeMux(),
		pages:   pages,
	}
	s.routes()
	return s, nil
}

// routes registers the handlers.
//
// The patterns name their method, so anything but GET on a register URL gets a
// 405 from the mux rather than a handler that has to check.
func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleRoot)
	s.mux.HandleFunc("GET /accounts/{id}", s.handleRegister)
	// The entry has its own address rather than a POST to the register's, so an
	// unmatched method on a register URL still gets a 405 from the mux.
	s.mux.HandleFunc("POST /accounts/{id}/transactions", s.handleCreateTransaction)
	s.mux.HandleFunc("POST /accounts/{id}/transactions/{txn}/status", s.handleSetStatus)
	// The catch-all is last in precedence, not in registration: ServeMux picks
	// the most specific pattern. It exists so a mistyped address gets a page
	// that says what to do next rather than net/http's bare "404 page not
	// found" (SPECIFICATION.md RG-4).
	s.mux.HandleFunc("GET /", s.handleNotFound)

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
		// The layout is not a page, and the problem page is not built on the
		// layout: see problem.gohtml for why it stands alone.
		if base == layoutFile || base == problemFile {
			continue
		}
		t, err := template.New(layoutFile).ParseFS(templateFS, "templates/"+layoutFile, name)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", base, err)
		}
		pages[base] = t
	}

	for _, required := range []string{"register.gohtml", "empty.gohtml", "error.gohtml"} {
		if pages[required] == nil {
			return nil, fmt.Errorf("web: missing template %s", required)
		}
	}
	return pages, nil
}

const (
	layoutFile = "layout.gohtml"

	// problemFile is rendered by NewProblem when there is no database to serve a
	// register from. It is parsed on its own, not with the layout.
	problemFile = "problem.gohtml"
)

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
// them; the layout copes.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, accounts []account.Account, heading, detail, nextStep string) {
	s.render(w, r, status, "error.gohtml", errorPage{
		layout: layout{
			Title:    heading,
			Database: s.store.Path(),
			Version:  s.version,
			Accounts: accounts,
		},
		Heading:  heading,
		Detail:   detail,
		NextStep: nextStep,
	})
}
