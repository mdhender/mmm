// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/storage"
)

// openFile gives a checkbook on disk, which is what a backup needs.
func openFile(t *testing.T) (*storage.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "checkbook.db")
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

// post sends a form the way a page this program served would: same-origin, and
// with the headers a browser fills in by itself.
func postFromPage(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Origin", "http://"+r.Host)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestBackupNowWritesAVerifiedCopy is BK-2 and BK-5 from the browser: the action
// exists, it writes a file beside the checkbook, and the file opens.
func TestBackupNowWritesAVerifiedCopy(t *testing.T) {
	store, path := openFile(t)
	seed(t, store)
	h := server(t, store)

	w := postFromPage(t, h, "/backup", url.Values{"return": {"/accounts/1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", w.Code, w.Body.String())
	}

	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc.Path != "/accounts/1" {
		t.Errorf("Location path = %q, want the page the reader was on", loc.Path)
	}
	name := loc.Query().Get("backedup")
	if name == "" {
		t.Fatal("the redirect does not name the backup that was written")
	}

	written := filepath.Join(filepath.Dir(path), name)
	if _, err := os.Stat(written); err != nil {
		t.Fatalf("the named backup is not there: %v", err)
	}
	copyStore, err := storage.Open(t.Context(), written)
	if err != nil {
		t.Fatalf("the backup does not open as a checkbook: %v", err)
	}
	_ = copyStore.Close()

	// And the reader is told, on whatever page they land on.
	body := get(t, h, loc.String()).Body.String()
	if !strings.Contains(body, name) {
		t.Error("the page the reader lands on does not name the backup")
	}
	if !strings.Contains(body, "A backup was written") {
		t.Error("the page the reader lands on does not say a backup was written")
	}
}

// TestBackupNoticeIsOnlyShownForANameWeWrote: the name arrives in a URL, which
// anything can compose, and it goes into a sentence the reader is meant to
// believe.
func TestBackupNoticeIsOnlyShownForANameWeWrote(t *testing.T) {
	store := open(t)
	seed(t, store)
	h := server(t, store)

	body := get(t, h, "/accounts/1?backedup="+url.QueryEscape("your files are safe with us.exe")).Body.String()
	if strings.Contains(body, "A backup was written") {
		t.Error("a name this program never wrote was reported as a backup")
	}
}

// TestBackupRefusesTheDemo: -demo has nothing on disk, and sample data under a
// backup's name in the household's folder would be a lie.
func TestBackupRefusesTheDemo(t *testing.T) {
	store := open(t)
	seed(t, store)

	w := postFromPage(t, server(t, store), "/backup", url.Values{"return": {"/accounts/1"}})
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "nothing here to back up") {
		t.Errorf("page does not say why: %s", body)
	}
	if !strings.Contains(body, "What to do next") {
		t.Error("page does not say what to do next (RG-4)")
	}
}

// TestControlRoutesRefuseACrossSitePost. These act on the process and on the
// file rather than on a record, so a page on another site must not be able to
// fire one. It is two headers, not the session machinery PL-7 forbids.
func TestControlRoutesRefuseACrossSitePost(t *testing.T) {
	store, path := openFile(t)
	seed(t, store)
	h := server(t, store)

	for _, tt := range []struct {
		name    string
		headers map[string]string
	}{
		{"a form on another site", map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://example.invalid"}},
		{"an origin that is not ours", map[string]string{"Origin": "https://example.invalid"}},
		{"a same-site subresource", map[string]string{"Sec-Fetch-Site": "same-site"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/backup", strings.NewReader(""))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", w.Code)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				t.Fatalf("ReadDir: %v", err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "checkbook-2") {
					t.Fatalf("a refused request wrote %q", e.Name())
				}
			}
		})
	}
}

// TestBackupIsNotAGetRoute: a link, a prefetch, or an image must not be able to
// fire it.
//
// The answer is the catch-all's "no page at that address" rather than a 405,
// because GET / matches every address the mux has no better pattern for. What
// matters is that nothing was written.
func TestBackupIsNotAGetRoute(t *testing.T) {
	store, path := openFile(t)

	w := get(t, server(t, store), "/backup")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /backup = %d, want 404", w.Code)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "checkbook-2") {
			t.Fatalf("a GET wrote %q", e.Name())
		}
	}
}

// TestBackUpNowIsOnEveryPage is BK-2: the action has to be visible, and the
// frame is what makes it visible from wherever the reader happens to be.
func TestBackUpNowIsOnEveryPage(t *testing.T) {
	store, _ := openFile(t)
	seed(t, store)
	h := server(t, store)

	for _, path := range []string{"/accounts/1", "/accounts/new", "/accounts/99"} {
		body := get(t, h, path).Body.String()
		if !strings.Contains(body, "This checkbook") {
			t.Errorf("%s has no checkbook section in the frame", path)
		}
		if !strings.Contains(body, "Back up now") {
			t.Errorf("%s does not offer Back up now (BK-2)", path)
		}
		// A form, not a link: a link would be fired by a prefetch, and the
		// same-origin check would have nothing to read.
		if !strings.Contains(body, `<form method="post" action="/backup">`) {
			t.Errorf("%s offers Back up now as something other than a POST", path)
		}
	}
}

// TestBackUpNowIsWithheldFromTheDemo: the sample household has no file behind
// it, and an action that can only be refused is worse than one that is not
// offered.
func TestBackUpNowIsWithheldFromTheDemo(t *testing.T) {
	store := open(t)
	seed(t, store)

	body := get(t, server(t, store), "/accounts/1").Body.String()
	if strings.Contains(body, "Back up now") {
		t.Error("the demo offers to back up a database that is not on disk")
	}
}
