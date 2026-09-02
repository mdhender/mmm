// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mdhender/mmm/internal/web"
)

// TestQuitAsksFirst is RG-3. Ending the program takes the register away from
// every window the household has open.
func TestQuitAsksFirst(t *testing.T) {
	store, path := openFile(t)
	var fired atomic.Int64
	h := newServer(t, web.Options{Store: store, Quit: func() { fired.Add(1) }})

	body := get(t, h, "/quit").Body.String()
	if !strings.Contains(body, "Quit Checkbook?") {
		t.Error("the quit link does not ask first")
	}
	if !strings.Contains(body, path) {
		t.Error("the confirmation does not say which checkbook is open")
	}
	if !strings.Contains(body, "Keep going") {
		t.Error("the confirmation offers no way out")
	}
	if fired.Load() != 0 {
		t.Fatal("asking about quitting quit")
	}
}

// TestQuitFiresOnceAndOnlyAfterTheAnswerIsWritten. The browser is about to lose
// the connection, so anything not already written will not arrive -- which is
// why quit() is the last statement in the handler and never earlier.
func TestQuitFiresOnceAndOnlyAfterTheAnswerIsWritten(t *testing.T) {
	store := open(t)

	var fired atomic.Int64
	var bodyAtQuit atomic.Value
	w := httptest.NewRecorder()

	h := newServer(t, web.Options{Store: store, Quit: func() {
		fired.Add(1)
		bodyAtQuit.Store(w.Body.String())
	}})

	r := httptest.NewRequest(http.MethodPost, "/quit", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Origin", "http://"+r.Host)
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if fired.Load() != 1 {
		t.Fatalf("quit fired %d times, want 1", fired.Load())
	}

	written, _ := bodyAtQuit.Load().(string)
	if !strings.Contains(written, "Checkbook has stopped") {
		t.Error("the program was stopped before the goodbye page had been written")
	}
	// It has to stand on its own: the stylesheet is a second request, and by the
	// time the browser makes it there is nothing left to answer.
	if strings.Contains(written, "/static/app.css") {
		t.Error("the goodbye page asks for a stylesheet from a server that is stopping")
	}
	if !strings.Contains(written, "<style>") {
		t.Error("the goodbye page carries no styles of its own")
	}
}

// TestQuitPrintsTheRestartHint. The command composes it, because it is the only
// thing that knows how it was started; this page only prints it.
func TestQuitPrintsTheRestartHint(t *testing.T) {
	store := open(t)
	h := newServer(t, web.Options{
		Store:   store,
		Quit:    func() {},
		Restart: web.RestartHint{Command: "go run ./cmd/checkbook -db /home/pat/checkbook.db", Directory: "/home/pat/src/mmm"},
	})

	body := postFromPage(t, h, "/quit", nil).Body.String()
	for _, want := range []string{"go run ./cmd/checkbook -db /home/pat/checkbook.db", "/home/pat/src/mmm"} {
		if !strings.Contains(body, want) {
			t.Errorf("the goodbye page does not carry %q", want)
		}
	}
}

// TestQuitIsWithheldWithoutAQuitFunction: an action the program cannot carry out
// is not offered, and its address does not answer.
func TestQuitIsWithheldWithoutAQuitFunction(t *testing.T) {
	store := open(t)
	h := newServer(t, web.Options{Store: store})

	if w := get(t, h, "/quit"); w.Code != http.StatusNotFound {
		t.Errorf("GET /quit without a quit function = %d, want the catch-all's 404", w.Code)
	}
	if w := postFromPage(t, h, "/quit", nil); w.Code == http.StatusOK {
		t.Error("POST /quit answered in a build with no quit function")
	}
}

// TestCrossSiteQuitIsRefusedAndFiresNothing. This is the reason control.go
// exists: any page on the internet can post a form to 127.0.0.1:8842, and until
// these actions arrived the worst that could do was write a transaction.
func TestCrossSiteQuitIsRefusedAndFiresNothing(t *testing.T) {
	store := open(t)
	var fired atomic.Int64
	h := newServer(t, web.Options{Store: store, Quit: func() { fired.Add(1) }})

	r := httptest.NewRequest(http.MethodPost, "/quit", strings.NewReader(url.Values{}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Origin", "https://example.invalid")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if fired.Load() != 0 {
		t.Fatal("a cross-site post stopped the program")
	}
}
