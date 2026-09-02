// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/web"
)

// generationOf reads the token the close form carries, so a test presses the
// button the page actually drew rather than guessing a number.
func generationOf(t *testing.T, h http.Handler) string {
	t.Helper()

	body := get(t, h, "/checkbook/close").Body.String()
	_, rest, ok := strings.Cut(body, `name="generation" value="`)
	if !ok {
		t.Fatalf("the close page carries no generation:\n%s", body)
	}
	gen, _, ok := strings.Cut(rest, `"`)
	if !ok {
		t.Fatal("the generation field is not closed")
	}
	return gen
}

// memoryOpener answers with a fresh in-memory checkbook, which is what a test
// wants an Opener to do: it is the command that knows about files and sample
// data, not this package.
func memoryOpener(t *testing.T) web.Opener {
	t.Helper()

	var n int
	var mu sync.Mutex
	return func(ctx context.Context, req web.OpenRequest) (*storage.Store, error) {
		mu.Lock()
		n++
		name := strings.ReplaceAll(t.Name(), "/", "-") + "-opened-" + strconv.Itoa(n)
		mu.Unlock()
		return storage.OpenMemory(ctx, name)
	}
}

// TestCloseAsksFirst is RG-3. Closing takes the register away from every window
// the household has open, so it is not something one keystroke does.
func TestCloseAsksFirst(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := server(t, store)

	body := get(t, h, "/checkbook/close").Body.String()
	if !strings.Contains(body, "Close this checkbook?") {
		t.Error("the close link does not ask before closing")
	}
	if !strings.Contains(body, `action="/checkbook/close"`) {
		t.Error("the confirmation offers no way to go through with it")
	}
	if !strings.Contains(body, "Keep it open") {
		t.Error("the confirmation offers no way out")
	}
	// Asking is not doing.
	if get(t, h, "/accounts/1").Code != http.StatusOK {
		t.Error("the register stopped working after the confirmation was shown")
	}
}

// TestCloseThenTheReaderIsToldWhatToDoNext: the payoff of closing is that the
// file is safe to copy, and the page says so and offers to do it.
func TestCloseAndTheFileIsOfferedForBackup(t *testing.T) {
	store, path := openFile(t)
	seed(t, store)
	h := server(t, store)

	w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}})
	if w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"No checkbook is open", path, "safe to copy", "Back up now"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page after closing does not contain %q", want)
		}
	}

	// Every other tab now finds a page this program wrote, rather than an error
	// from the database or a blank 500.
	for _, address := range []string{"/", "/accounts/1", "/accounts/new", "/nowhere"} {
		w := get(t, h, address)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s after closing = %d, want 503", address, w.Code)
		}
		if !strings.Contains(w.Body.String(), "No checkbook is open") {
			t.Errorf("GET %s after closing did not get the no-checkbook page", address)
		}
	}

	// And the action that was the point of closing still works, on the file that
	// is no longer open.
	w = postFromPage(t, h, "/backup", url.Values{"return": {"/checkbook"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("backup after closing = %d, want 303: %s", w.Code, w.Body.String())
	}
	name := mustQuery(t, w.Header().Get("Location"), "backedup")
	if _, err := storage.Open(t.Context(), filepath.Join(filepath.Dir(path), name)); err != nil {
		t.Errorf("the backup taken after closing does not open: %v", err)
	}
}

// TestStaleCloseIsRefused is CO-3's shape applied to the checkbook itself: a tab
// that has been on screen since before a swap must not close a database it was
// never looking at.
func TestStaleCloseIsRefused(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := newServer(t, web.Options{Store: store, Open: memoryOpener(t)})

	stale := generationOf(t, h)

	// Another window opens a different checkbook.
	if w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"elsewhere.db"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("open = %d, want 303: %s", w.Code, w.Body.String())
	}

	w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {stale}})
	if w.Code != http.StatusConflict {
		t.Fatalf("stale close = %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "changed in another window") {
		t.Error("the refusal does not say what happened")
	}
	// And the checkbook that is open was left alone.
	if get(t, h, "/").Code == http.StatusServiceUnavailable {
		t.Error("a stale close closed the checkbook that was open")
	}
}

// TestSecondCloseIsAnsweredAsASuccess: two tabs pressing Close is two people
// agreeing, not a conflict. The second is told what it wanted to hear.
func TestSecondCloseIsAnsweredAsASuccess(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := server(t, store)

	gen := generationOf(t, h)
	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {gen}}); w.Code != http.StatusOK {
		t.Fatalf("first close = %d, want 200", w.Code)
	}
	w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {gen}})
	if w.Code != http.StatusOK {
		t.Fatalf("second close = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "No checkbook is open") {
		t.Error("the second close did not answer with the no-checkbook page")
	}
}

// TestFailedOpenLeavesNothingOpen. current must never hold a Store whose pool is
// closed, and the reader must be told why with the words DescribeOpenError
// already uses for that sentinel (RG-4).
func TestFailedOpenLeavesNothingOpen(t *testing.T) {
	store := open(t)
	h := newServer(t, web.Options{
		Store: store,
		Open: func(ctx context.Context, req web.OpenRequest) (*storage.Store, error) {
			return nil, fmt.Errorf("%s: %w", req.Path, storage.ErrMissingDirectory)
		},
	})

	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}

	w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"/nowhere/checkbook.db"}})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failed open = %d, want 422", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "No checkbook is open") {
		t.Error("a failed open left something looking open")
	}
	if !strings.Contains(body, "/nowhere/checkbook.db") {
		t.Error("the page does not name what it tried to open")
	}
	// DescribeOpenError's own words for that sentinel, so the advice a reader
	// gets does not depend on which page they are standing on (RG-4).
	if !strings.Contains(body, "That folder does not exist") {
		t.Error("the page does not use the heading that sentinel already has")
	}
	if !strings.Contains(body, "Create the folder yourself") {
		t.Error("the page does not say what to do next")
	}

	// Nothing is open, and the register still answers with a page of ours.
	if get(t, h, "/").Code != http.StatusServiceUnavailable {
		t.Error("something was left open after a failed open")
	}
}

// TestOpenIsWithheldWithoutAnOpener: an action the program cannot carry out is
// not offered and its address does not answer.
func TestOpenIsWithheldWithoutAnOpener(t *testing.T) {
	store := open(t)
	h := newServer(t, web.Options{Store: store})

	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	if body := get(t, h, "/checkbook").Body.String(); strings.Contains(body, "Open a checkbook") {
		t.Error("a build with no opener offers to open one")
	}

	// 405 rather than 404: the address falls to the catch-all, which is a GET
	// route, so the mux answers on the method. What matters is that the handler
	// does not exist and nothing was opened.
	w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"anything.db"}})
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /checkbook/open without an opener = %d, want 405", w.Code)
	}
	if get(t, h, "/").Code != http.StatusServiceUnavailable {
		t.Error("something was opened by a build with no opener")
	}
}

// TestOpenAgainAfterClosing is the ritual this whole slice exists for: close,
// copy the file, open it again, without ever finding a terminal.
func TestOpenAgainAfterClosing(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := newServer(t, web.Options{Store: store, Open: memoryOpener(t)})

	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"checkbook.db"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("open = %d, want 303: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
	if code := get(t, h, "/").Code; code == http.StatusServiceUnavailable {
		t.Error("nothing is open after a successful open")
	}
}

// TestCloseUnderLoad is the load-bearing test, and it is meant to be run with
// -race: readers hammering a register while another window closes and opens.
//
// Nothing may panic, and every answer must be one of this program's pages --
// never a blank 500, and never SQLite's "pool closed" dressed up as a fault in
// the reader's account list.
func TestCloseUnderLoad(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := newServer(t, web.Options{Store: store, Open: memoryOpener(t)})

	stop := make(chan struct{})
	var readers sync.WaitGroup
	var bad []string
	var mu sync.Mutex

	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, address := range []string{"/", "/accounts/1", "/accounts/new"} {
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, address, nil))

					body := w.Body.String()
					ours := strings.Contains(body, "No checkbook is open") ||
						strings.Contains(body, "<!doctype html>") ||
						w.Code == http.StatusFound ||
						w.Code == http.StatusSeeOther
					if !ours || strings.Contains(body, "error while listing accounts") {
						mu.Lock()
						bad = append(bad, address+" -> "+strconv.Itoa(w.Code)+": "+firstLine(body))
						mu.Unlock()
					}
				}
			}
		}()
	}

	for range 5 {
		if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK && w.Code != http.StatusConflict {
			t.Errorf("close = %d", w.Code)
		}
		if w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"checkbook.db"}}); w.Code != http.StatusSeeOther {
			t.Errorf("open = %d: %s", w.Code, w.Body.String())
		}
	}

	close(stop)
	readers.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(bad) > 0 {
		t.Fatalf("%d answers were not one of ours; the first is %s", len(bad), bad[0])
	}
}

// firstLine trims a body down to something a failure message can carry.
func firstLine(body string) string {
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[:i]
	}
	if len(body) > 120 {
		body = body[:120]
	}
	return body
}

// mustQuery reads one parameter out of a Location header.
func mustQuery(t *testing.T, location, key string) string {
	t.Helper()

	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("Location %q: %v", location, err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("Location %q carries no %s", location, key)
	}
	return v
}

// TestTheDemoCanBeOpenedTwice. A shared in-memory database survives as long as
// one connection is open, so a second open that seeded it again would collide on
// a duplicate account name. The one in memory is therefore closed before
// anything else is opened -- it keeps nothing, so there is nothing to protect by
// holding it open.
func TestTheDemoCanBeOpenedTwice(t *testing.T) {
	store := open(t)
	name := strings.ReplaceAll(t.Name(), "/", "-") + "-demo"

	h := newServer(t, web.Options{
		Store: store,
		Open: func(ctx context.Context, req web.OpenRequest) (*storage.Store, error) {
			demo, err := storage.OpenMemory(ctx, name)
			if err != nil {
				return nil, err
			}
			if _, err := account.Create(ctx, demo, account.New{
				Name: "Checking", Type: account.Checking, Currency: money.USD,
			}); err != nil {
				_ = demo.Close()
				return nil, err
			}
			return demo, nil
		},
	})

	for attempt := 1; attempt <= 2; attempt++ {
		w := postFromPage(t, h, "/checkbook/open", url.Values{"demo": {"1"}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("open %d = %d, want 303: %s", attempt, w.Code, w.Body.String())
		}
	}
}

// webOptionsWithOpener is the shape most control-route tests want: a store to
// start from and an opener that hands back in-memory checkbooks.
func webOptionsWithOpener(t *testing.T, store *storage.Store) web.Options {
	t.Helper()
	return web.Options{Store: store, Open: memoryOpener(t)}
}
