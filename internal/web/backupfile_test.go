// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/web"
)

// fileOpener is the Opener the command supplies, minus the flags and the sample
// data: it opens a real file, read-only when asked and read-only anyway when
// the file turns out to be a backup. These tests are about what the browser
// does with a backup, which needs a backup on disk.
func fileOpener(t *testing.T) web.Opener {
	t.Helper()

	return func(ctx context.Context, req web.OpenRequest) (*storage.Store, error) {
		if req.ReadOnly {
			return storage.OpenReadOnly(ctx, req.Path)
		}
		return storage.OpenOrReadOnly(ctx, req.Path)
	}
}

// withBackup gives a server on a seeded checkbook with a real backup beside it,
// and returns the handler and the backup's path.
func withBackup(t *testing.T) (*web.Server, string) {
	t.Helper()

	store, path := openFile(t)
	seed(t, store)

	res, err := backup.Create(t.Context(), path, filepath.Dir(path))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return newServer(t, web.Options{Store: store, Open: fileOpener(t)}), res.Path
}

// closeCheckbook presses Close and confirms, which is what a reader does before
// opening or restoring anything else.
func closeCheckbook(t *testing.T, h *web.Server) {
	t.Helper()

	w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}})
	if w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// TestOpeningABackupOpensItReadOnly is the bug this rule was written for, from
// the browser: the box was there and unticked, and the register came up on a
// backup which was then migrated and written to. It is still never written to --
// but the answer is now to open it the one way it can be opened rather than to
// refuse it and ask the reader to say what the file already says about itself.
func TestOpeningABackupOpensItReadOnly(t *testing.T) {
	h, path := withBackup(t)
	closeCheckbook(t, h)

	// The box is not ticked, and the reader is not assumed to know this is a
	// backup. The header says so, which is enough.
	w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {path}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("opening a backup = %d, want 303: %s", w.Code, w.Body.String())
	}

	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, "Backup &mdash; nothing can be changed") &&
		!strings.Contains(body, "Backup — nothing can be changed") {
		t.Error("the frame does not say this is a backup rather than merely read-only")
	}

	// And the file is untouched: no -wal beside it, which is what opening one
	// for writing would have left (BK-6).
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); err == nil {
			t.Errorf("%s was created, so the backup was opened for writing", filepath.Base(sidecar))
		}
	}
}

// TestABackupOpensReadOnlyAndSaysSo. Refusing to write to one is only half the
// rule; reading it is how a household decides it is the backup they want.
func TestABackupOpensReadOnly(t *testing.T) {
	h, path := withBackup(t)
	closeCheckbook(t, h)

	w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {path}, "readonly": {"1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("opening a backup read-only = %d, want 303: %s", w.Code, w.Body.String())
	}

	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, "Backup &mdash; nothing can be changed") &&
		!strings.Contains(body, "Backup — nothing can be changed") {
		t.Error("the frame does not say this is a backup rather than merely read-only")
	}
	// The records are readable, which is the point of opening it at all.
	if !strings.Contains(body, "Riba Smith") {
		t.Error("the backup's register is not shown")
	}
	// And the reader is told where the next step is, rather than left at a dead
	// end with a padlock.
	if !strings.Contains(body, "Restoring is offered on the page you land on") {
		t.Error("the sidebar does not say where restoring is")
	}
}

// TestRestoreFromTheBrowser is BK-4 from the browser: the backup becomes a
// checkbook at a name the reader chose, and the backup is left as it was.
func TestRestoreFromTheBrowser(t *testing.T) {
	h, path := withBackup(t)
	closeCheckbook(t, h)

	dest := filepath.Join(filepath.Dir(path), "restored.db")
	w := postFromPage(t, h, "/checkbook/restore/copy", url.Values{"source": {path}, "dest": {dest}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303: %s", w.Code, w.Body.String())
	}

	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the restored checkbook is not there: %v", err)
	}
	if id, err := storage.ApplicationID(dest); err != nil || id != storage.AppID {
		t.Errorf("the restored file's application id = %d, %v; want a checkbook's", id, err)
	}
	if id, err := storage.ApplicationID(path); err != nil || id != storage.BackupAppID {
		t.Errorf("the backup's application id = %d, %v; want it left as a backup", id, err)
	}

	// The page the reader lands on says what happened and offers to open it.
	body := get(t, h, w.Header().Get("Location")).Body.String()
	if !strings.Contains(body, "was restored to") {
		t.Error("the page the reader lands on does not say the backup was restored")
	}
	if !strings.Contains(body, `value="`+dest+`"`) {
		t.Error("the open box is not filled in with the restored checkbook")
	}

	// And it opens for writing, which is the whole difference.
	w = postFromPage(t, h, "/checkbook/open", url.Values{"path": {dest}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("opening the restored checkbook = %d, want 303: %s", w.Code, w.Body.String())
	}
	if body := get(t, h, "/accounts/1").Body.String(); !strings.Contains(body, "Add a transaction") {
		t.Error("the restored checkbook does not offer the entry form, so it did not open for writing")
	}
}

// TestRestoreNeverWritesOverAFile, said to the reader in words they can act on.
func TestRestoreRefusalsSayWhatToDoNext(t *testing.T) {
	h, path := withBackup(t)
	closeCheckbook(t, h)

	dir := filepath.Dir(path)
	for _, tt := range []struct {
		name   string
		source string
		dest   string
		want   string
	}{
		{"over a file that is there", path, path, "already a file at"},
		{"from a file that is not", filepath.Join(dir, "nowhere.db"), filepath.Join(dir, "a.db"), "There is no file at"},
		{"with no source", "", filepath.Join(dir, "b.db"), "Type the path of the backup"},
		{"with no destination", path, "", "Type the path to restore to"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := postFromPage(t, h, "/checkbook/restore/copy", url.Values{"source": {tt.source}, "dest": {tt.dest}})
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, tt.want) {
				t.Errorf("the refusal does not contain %q:\n%s", tt.want, body)
			}
			if !strings.Contains(body, "Nothing was written") && !strings.Contains(body, "Nothing is ever written over") {
				t.Error("the refusal does not say that nothing was written")
			}
		})
	}
}

// TestClosingABackupOffersToRestoreIt rather than to back it up: a backup of a
// backup is not what anybody came to that page for.
func TestClosingABackupOffersToRestoreIt(t *testing.T) {
	h, path := withBackup(t)
	closeCheckbook(t, h)

	if w := postFromPage(t, h, "/checkbook/open", url.Values{"path": {path}, "readonly": {"1"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("opening the backup read-only = %d, want 303", w.Code)
	}
	closeCheckbook(t, h)

	body := get(t, h, "/checkbook").Body.String()
	if strings.Contains(body, "Back up now") {
		t.Error("the page offers to back up a backup")
	}
	if !strings.Contains(body, "That was a backup, opened for reading") {
		t.Error("the page does not say what was closed")
	}
	// The restore form opens on the file they were just reading.
	if !strings.Contains(body, `name="source" value="`+path+`"`) {
		t.Error("the restore form is not filled in with the backup that was just closed")
	}
}

// TestRestoreIsOfferedWithoutAnOpener: restoring to a file of its own writes a
// third file and swaps nothing, so unlike Open it needs nothing injected and is
// always honest. The one press that replaces the checkbook does need an Opener,
// and it is withheld with the reason rather than offered and then refused.
func TestRestoreIsOfferedWithoutAnOpener(t *testing.T) {
	store, _ := openFile(t)
	h := newServer(t, web.Options{Store: store})

	closeCheckbook(t, h)
	body := get(t, h, "/checkbook").Body.String()
	if !strings.Contains(body, `action="/checkbook/restore/copy"`) {
		t.Error("restoring to a copy is not offered when the program cannot open a checkbook")
	}
	if !strings.Contains(body, "This build cannot open a checkbook by itself") {
		t.Error("the page does not say why the one-press restore is not offered")
	}
}
