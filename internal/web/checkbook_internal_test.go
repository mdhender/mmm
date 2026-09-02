// Copyright (c) 2026 Michael D Henderson.

package web

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdhender/mmm/internal/storage"
)

// testServer builds a server over an in-memory checkbook. These tests are inside
// the package because what they check -- the lease, the retire, the closer -- is
// deliberately not exported.
func testServer(t *testing.T) *Server {
	t.Helper()

	store, err := storage.OpenMemory(t.Context(), strings.ReplaceAll(t.Name(), "/", "-"))
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	s, err := New(Options{Store: store, Version: "0.0.0-test", Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		_ = store.Close()
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestRetireHandsTheCheckbookToExactlyOneCaller is what makes Close exactly
// once. A second Close on the same pool reports "already closed", and there is
// nothing sensible to do with that, so the pointer swap has to be the thing that
// decides who closes.
func TestRetireHandsTheCheckbookToExactlyOneCaller(t *testing.T) {
	s := testServer(t)

	const callers = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	done.Add(callers)

	var mu sync.Mutex
	var got []*checkbook

	for range callers {
		go func() {
			defer done.Done()
			start.Wait()
			if cb, ok := s.retire(0); ok {
				mu.Lock()
				got = append(got, cb)
				mu.Unlock()
			}
		}()
	}
	start.Done()
	done.Wait()

	if len(got) != 1 {
		t.Fatalf("%d callers were handed the checkbook, want exactly 1", len(got))
	}
	if err := s.closeRetired(got[0]); err != nil {
		t.Errorf("closeRetired: %v", err)
	}
}

// TestCloseIsNotCoupledToRequestLifetime is the load-bearing property of the
// whole design: the closer waits on connection lifetime, which Close can force
// to an end, and never on request lifetime, which has no bound.
//
// A handler is parked inside its lease. Close must still answer at once, the
// checkbook must be gone from the moment it does, and the next request must get
// the page that says so.
func TestCloseIsNotCoupledToRequestLifetime(t *testing.T) {
	s := testServer(t)

	parked := make(chan struct{})
	arrived := make(chan struct{})
	s.mux.HandleFunc("GET /test/slow", s.withCheckbook(func(w http.ResponseWriter, r *http.Request, cb *checkbook) {
		close(arrived)
		<-parked
		w.WriteHeader(http.StatusOK)
	}))

	go func() {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test/slow", nil))
	}()
	<-arrived

	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/checkbook/close",
			strings.NewReader("generation="+strconv.FormatUint(s.currentCheckbook().gen, 10)))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		answered <- w
	}()

	select {
	case w := <-answered:
		if w.Code != http.StatusOK {
			t.Fatalf("close answered %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "No checkbook is open") {
			t.Error("close did not answer with the no-checkbook page")
		}
	case <-time.After(2 * time.Second):
		close(parked)
		t.Fatal("close waited for a request that had not finished")
	}

	// And the checkbook is gone from the moment the answer was written, not
	// from whenever the parked request happens to end.
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("GET / after close = %d, want 503", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No checkbook is open") {
		t.Error("GET / after close did not get the no-checkbook page")
	}

	close(parked)
}
