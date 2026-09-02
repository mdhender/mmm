// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/web"
)

// TestDescribeOpenErrorCoversEveryFailure checks that each way of failing to open
// a database gets its own page, and that none of them is a dead end.
func TestDescribeOpenErrorCoversEveryFailure(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantHeading string
		wantDoc     string
	}{
		{
			"newer version",
			fmt.Errorf("checkbook.db: %w: database is at schema 47, this program understands 3", storage.ErrDatabaseTooNew),
			"newer version",
			"upgrade-the-application",
		},
		{
			"not a checkbook",
			fmt.Errorf("other.db: %w: application_id = 0x0", storage.ErrNotCheckbook),
			"not a checkbook",
			"create-your-first-checkbook",
		},
		{
			// A backup is one of ours, so "not a checkbook" would be both wrong
			// and useless: the reader has two things they can do with it.
			"a backup",
			fmt.Errorf("checkbook-20260902-141530.db: %w", storage.ErrIsBackup),
			"is a backup",
			"restore-a-backup",
		},
		{
			"written by an older version",
			fmt.Errorf("checkbook-20240101-120000.db: %w: database is at schema 1, this program expects 3", storage.ErrDatabaseTooOld),
			"older version",
			"restore-a-backup",
		},
		{
			"no file there",
			fmt.Errorf("/nowhere/checkbook.db: %w", storage.ErrMissingFile),
			"no file there",
			"create-your-first-checkbook",
		},
		{
			"missing directory",
			fmt.Errorf("/nowhere: %w", storage.ErrMissingDirectory),
			"folder does not exist",
			"create-your-first-checkbook",
		},
		{
			"schema behind",
			fmt.Errorf("checkbook.db: %w: database is at schema 1", storage.ErrSchemaVersion),
			"schema",
			"report-a-problem",
		},
		{
			// Anything unrecognized still has to say what to do next: a reader
			// facing an unknown failure needs the advice more, not less.
			"unrecognized",
			errors.New("disk I/O error"),
			"could not be opened",
			"report-a-problem",
		},
	}

	headings := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := web.DescribeOpenError(tt.err, "/tmp/checkbook.db")

			if !strings.Contains(got.Heading, tt.wantHeading) {
				t.Errorf("heading = %q, want it to contain %q", got.Heading, tt.wantHeading)
			}
			if len(got.Steps) == 0 {
				t.Error("no next step: an error page without one is a dead end (RG-4)")
			}
			if len(got.Docs) == 0 {
				t.Error("no documents to read")
			}
			if got.Database != "/tmp/checkbook.db" {
				t.Errorf("database = %q, want the path it was given", got.Database)
			}
			// The underlying message has to survive: it is what goes into a
			// problem report.
			if !strings.Contains(got.Detail, tt.err.Error()) {
				t.Errorf("detail = %q, want it to contain the error", got.Detail)
			}

			var found bool
			for _, d := range got.Docs {
				if strings.Contains(d.Path, tt.wantDoc) {
					found = true
				}
				if !strings.HasSuffix(d.URL, d.Path) {
					t.Errorf("doc %q: URL %q does not end in its path %q", d.Title, d.URL, d.Path)
				}
			}
			if !found {
				t.Errorf("docs %v do not include %q", got.Docs, tt.wantDoc)
			}

			// Two different failures that produce the same heading would leave
			// the reader unable to tell which one they have.
			if prev, ok := headings[got.Heading]; ok {
				t.Errorf("heading %q is shared with the %q case", got.Heading, prev)
			}
			headings[got.Heading] = tt.name
		})
	}
}

// startupFailure builds the server the program serves when -db would not open:
// the ordinary one, with no store and the failure handed over as a Problem.
func startupFailure(t *testing.T, err error) http.Handler {
	t.Helper()

	p := web.DescribeOpenError(err, "/tmp/checkbook.db")
	return newServer(t, web.Options{
		Problem:       &p,
		CheckbookPath: "/tmp/checkbook.db",
		Open:          memoryOpener(t),
	})
}

// TestStartupFailureAnswersEveryAddress. Whatever the reader asks for, including
// a bookmarked register, they get the explanation rather than a 404 that would
// send them looking for the wrong problem. Unlike the page this replaced, the
// addresses that can do something about it still answer.
func TestStartupFailureAnswersEveryAddress(t *testing.T) {
	h := startupFailure(t, fmt.Errorf("%w: schema 47", storage.ErrDatabaseTooNew))

	for _, path := range []string{"/", "/accounts/1", "/accounts/nope", "/anything/at/all"} {
		w := get(t, h, path)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("GET %s: status = %d, want 503", path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "newer version of the program") {
			t.Errorf("GET %s: did not explain why there is no register", path)
		}
	}
}

// TestStartupFailurePageContents. The page says which file, what happened, what
// to do next, and which build produced it -- everything a report needs (RG-4).
func TestStartupFailurePageContents(t *testing.T) {
	h := startupFailure(t, fmt.Errorf("%w: database is at schema 47", storage.ErrDatabaseTooNew))
	body := get(t, h, "/checkbook").Body.String()

	for _, want := range []string{
		"/tmp/checkbook.db",                      // which file
		"What to do next",                        // RG-4
		"restore the backup",                     // a step
		"docs/how-to/upgrade-the-application.md", // where to read more
		"database is at schema 47",               // the message, for a report
		"0.0.0-test",                             // which build
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not contain %q", want)
		}
	}

	// The page must not carry the register's frame: there is no database to
	// build an account list from, and an empty sidebar reading "None yet." would
	// suggest an empty checkbook rather than one that could not be opened.
	if strings.Contains(body, "None yet.") || strings.Contains(body, `aria-label="Accounts"`) {
		t.Error("the page rendered the account list")
	}
}

// TestStartupFailureStopsBeingWhatIsWrong. Once something has opened, the reason
// -db would not is history, and a page that kept saying it would be describing a
// state the reader has left behind.
func TestStartupFailureStopsBeingWhatIsWrong(t *testing.T) {
	h := startupFailure(t, fmt.Errorf("%w: schema 47", storage.ErrDatabaseTooNew))

	if w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {"elsewhere.db"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("open = %d, want 303: %s", w.Code, w.Body.String())
	}
	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}
	if body := get(t, h, "/checkbook").Body.String(); strings.Contains(body, "newer version of the program") {
		t.Error("the page still reports the startup failure after a checkbook was opened")
	}
}

// TestStylesheetIsServedWithNothingOpen: the page has to be legible, so the one
// asset it references is not caught by the catch-all.
func TestStylesheetIsServedWithNothingOpen(t *testing.T) {
	h := startupFailure(t, errors.New("disk I/O error"))

	if w := get(t, h, "/static/app.css"); w.Code != http.StatusOK {
		t.Fatalf("stylesheet status = %d, want 200", w.Code)
	}
}

// TestProblemDocsExist follows every document the problem page offers.
//
// This is the one page a reader reaches when nothing else works, so a link on it
// that goes nowhere is worse than no link. The paths are repo-relative, and this
// test is what notices when a document is renamed or moved.
func TestProblemDocsExist(t *testing.T) {
	seen := make(map[string]bool)
	for _, err := range []error{
		fmt.Errorf("%w", storage.ErrDatabaseTooNew),
		fmt.Errorf("%w", storage.ErrNotCheckbook),
		fmt.Errorf("%w", storage.ErrMissingDirectory),
		fmt.Errorf("%w", storage.ErrSchemaVersion),
		errors.New("disk I/O error"),
	} {
		for _, d := range web.DescribeOpenError(err, "checkbook.db").Docs {
			if seen[d.Path] {
				continue
			}
			seen[d.Path] = true
			if _, statErr := os.Stat(filepath.Join("..", "..", d.Path)); statErr != nil {
				t.Errorf("%q points at %s, which does not exist: %v", d.Title, d.Path, statErr)
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("no documents are offered by any problem page")
	}
}
