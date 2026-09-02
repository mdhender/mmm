// Copyright (c) 2026 Michael D Henderson.

package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mdhender/mmm/internal/account"
	"github.com/mdhender/mmm/internal/backup"
	"github.com/mdhender/mmm/internal/money"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/transaction"
	"github.com/mdhender/mmm/internal/web"
)

// restorable builds the situation the whole slice is about: a checkbook on disk
// with records in it, a backup taken from it, and a server that can open files.
// The backup is taken before the second transaction, so a restore is visible as
// a transaction disappearing.
func restorable(t *testing.T) (h *web.Server, path, backupPath string) {
	t.Helper()

	store, path := openFile(t)
	acct := seed(t, store)
	h = newServer(t, web.Options{Store: store, Open: fileOpener(t), CheckbookPath: path})

	w := postFromPage(t, h, "/backup", url.Values{"return": {"/accounts/1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("backup = %d, want 303: %s", w.Code, w.Body.String())
	}
	backupPath = filepath.Join(backup.Folder(path), mustQuery(t, w.Header().Get("Location"), "backedup"))

	// Entered after the backup, so it is what a restore takes away.
	if _, err := transaction.Create(t.Context(), store, acct, transaction.New{
		Date: "2026-09-01", Payee: "After the backup", Amount: usd(t, "-9.99"),
	}); err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	return h, path, backupPath
}

// chosenPath reads the value the list drew for a given file, so a test presses
// the row the page actually offered rather than composing one.
func chosenPath(t *testing.T, h http.Handler, want string) string {
	t.Helper()

	body := get(t, h, "/checkbook/restore").Body.String()
	if !strings.Contains(body, `value="`+want+`"`) {
		t.Fatalf("the restore page does not offer %s:\n%s", want, body)
	}
	return want
}

// restoreNow does the whole press: confirm, then post.
func restoreNow(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()

	confirm := get(t, h, "/checkbook/restore/confirm?path="+url.QueryEscape(path))
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm = %d, want 200: %s", confirm.Code, confirm.Body.String())
	}
	return postFromPage(t, h, "/checkbook/restore", url.Values{
		"path":       {path},
		"generation": {generationIn(t, confirm.Body.String())},
	})
}

// landing follows the redirect a control action answers with, so a test reads
// the page the reader actually ends up on rather than the one hop before it.
func landing(t *testing.T, h http.Handler, w *httptest.ResponseRecorder) string {
	t.Helper()

	target := w.Header().Get("Location")
	for range 3 {
		got := get(t, h, target)
		if got.Code != http.StatusFound && got.Code != http.StatusSeeOther {
			return got.Body.String()
		}
		// The notice travels in the query string, and the register's own address
		// is where the redirect lands; carry it across the hop.
		next, err := url.Parse(got.Header().Get("Location"))
		if err != nil {
			t.Fatalf("Location %q: %v", got.Header().Get("Location"), err)
		}
		from, err := url.Parse(target)
		if err != nil {
			t.Fatalf("Location %q: %v", target, err)
		}
		if next.RawQuery == "" {
			next.RawQuery = from.RawQuery
		}
		target = next.String()
	}
	t.Fatalf("redirects did not settle, last was %q", target)
	return ""
}

// generationIn reads the token a page is carrying.
func generationIn(t *testing.T, body string) string {
	t.Helper()

	_, rest, ok := strings.Cut(body, `name="generation" value="`)
	if !ok {
		t.Fatalf("the page carries no generation:\n%s", body)
	}
	gen, _, ok := strings.Cut(rest, `"`)
	if !ok {
		t.Fatal("the generation field is not closed")
	}
	return gen
}

// TestRestorePageListsTheBackupsItFound. The list is the offer, and it names the
// folder it searched so a household that keeps their copies elsewhere can see at
// once that this is not where they are.
func TestRestorePageListsTheBackupsItFound(t *testing.T) {
	h, path, backupPath := restorable(t)

	w := get(t, h, "/checkbook/restore")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"Restore a backup",
		filepath.Dir(path), // where it looked
		backup.FolderName + "/" + filepath.Base(backupPath), // the copy, shown where it is
		`value="` + backupPath + `"`,                        // and offered
		"Restore this backup",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the restore page does not contain %q", want)
		}
	}
	// The checkbook itself is not a copy of itself.
	if strings.Contains(body, `value="`+path+`"`) {
		t.Error("the list offers the checkbook as something to restore from")
	}
}

// TestARegisterThatWouldNotOpenStillOffersRestore is the test the whole
// prerequisite exists for. A corrupt checkbook is the main reason to restore,
// and it used to be the one case that could reach no route at all: the failure
// was served by a mux whose "/" answered every address with one static page.
func TestARegisterThatWouldNotOpenStillOffersRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkbook.db")

	// A checkbook, a backup of it, and then the checkbook replaced by rubbish --
	// which is what a household finds on the morning this feature is for.
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := account.Create(t.Context(), store, account.New{
		Name: "Checking", Type: account.Checking, Currency: money.USD,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	res, err := backup.Create(t.Context(), path, dir)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(path, []byte("this is not a database"), 0o644); err != nil {
		t.Fatalf("corrupt the checkbook: %v", err)
	}

	// What the command does when openStore fails: the ordinary server, no store,
	// the failure handed over.
	problem := web.DescribeOpenError(storage.ErrNotCheckbook, path)
	h := newServer(t, web.Options{Open: fileOpener(t), CheckbookPath: path, Problem: &problem})

	body := get(t, h, "/checkbook").Body.String()
	if !strings.Contains(body, "not a checkbook") {
		t.Error("the page does not say why there is no register")
	}
	if !strings.Contains(body, `value="`+res.Path+`"`) {
		t.Fatalf("the page does not offer the backup that is sitting beside the file:\n%s", body)
	}

	// And one press recovers.
	if w := restoreNow(t, h, res.Path); w.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303: %s", w.Code, w.Body.String())
	}
	if code := get(t, h, "/accounts/1").Code; code != http.StatusOK {
		t.Fatalf("the register after restoring = %d, want 200", code)
	}
}

// TestRestorePageWorksWithNothingOpen. The page must not be wrapped in
// withCheckbook: that answers 503 when nothing is open, and nothing-open is the
// case it exists for.
func TestRestorePageWorksWithNothingOpen(t *testing.T) {
	h, _, backupPath := restorable(t)

	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {generationOf(t, h)}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}

	w := get(t, h, "/checkbook/restore")
	if w.Code != http.StatusOK {
		t.Fatalf("the restore page with nothing open = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `value="`+backupPath+`"`) {
		t.Error("the list is empty with nothing open")
	}
	// And the frame does not pretend there is an empty checkbook behind it.
	if strings.Contains(w.Body.String(), "None yet.") {
		t.Error("the sidebar reads as an empty checkbook rather than as no checkbook")
	}
}

// TestRestoreAsksBeforeReplacing is RG-3: the confirmation names both files,
// says what will be kept, offers a way out, and leaves the register answering.
func TestRestoreAsksBeforeReplacing(t *testing.T) {
	h, path, backupPath := restorable(t)

	body := get(t, h, "/checkbook/restore/confirm?path="+url.QueryEscape(backupPath)).Body.String()
	for _, want := range []string{
		filepath.Base(backupPath), // what it restores from
		path,                      // what it replaces
		"no way to merge the two",
		"checkbook-replaced-", // what the file that is kept will be called
		"Restore it",
		"Keep what I have",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the confirmation does not contain %q", want)
		}
	}
	// Asking is not doing.
	if !strings.Contains(get(t, h, "/accounts/1").Body.String(), "After the backup") {
		t.Error("the register changed after the confirmation was shown")
	}
}

// TestRestoreSwapsTheCheckbookInOnePress, and the register the reader lands on
// is the restored one.
func TestRestoreSwapsTheCheckbookInOnePress(t *testing.T) {
	h, path, backupPath := restorable(t)

	w := restoreNow(t, h, chosenPath(t, h, backupPath))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303: %s", w.Code, w.Body.String())
	}

	body := landing(t, h, w)
	if strings.Contains(body, "After the backup") {
		t.Error("the register still shows a transaction the backup does not have")
	}
	if !strings.Contains(body, "Riba Smith") {
		t.Error("the restored register does not show the records the backup had")
	}
	// The checkbook is still the checkbook: same name, and it opens for writing.
	if !strings.Contains(body, path) {
		t.Errorf("the frame does not name %s as the database in use", path)
	}
	if !strings.Contains(body, "Add a transaction") {
		t.Error("the restored checkbook does not offer the entry form, so it did not open for writing")
	}
}

// TestRestoreKeepsTheDisplacedCheckbook, and names it on the page the reader
// lands on. That name is their whole way back, so it is not something to leave
// in a log file.
func TestRestoreKeepsTheDisplacedCheckbook(t *testing.T) {
	h, path, backupPath := restorable(t)

	w := restoreNow(t, h, backupPath)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303: %s", w.Code, w.Body.String())
	}
	kept := mustQuery(t, w.Header().Get("Location"), "kept")
	if !backup.ValidReplacedName(kept) {
		t.Errorf("the displaced checkbook is named %q, which is not a name this program writes", kept)
	}

	body := landing(t, h, w)
	if !strings.Contains(body, kept) {
		t.Error("the page the reader lands on does not name the checkbook that was kept")
	}
	if !strings.Contains(body, "nothing was deleted") {
		t.Error("the page does not say that nothing was deleted")
	}

	// And it is a checkbook, with the transaction the restore took away still in
	// it -- openable directly, which is a shorter road back than a file that
	// must itself be restored (BK-1).
	keptPath := filepath.Join(filepath.Dir(path), kept)
	store, err := storage.Open(t.Context(), keptPath)
	if err != nil {
		t.Fatalf("the kept checkbook does not open: %v", err)
	}
	defer func() { _ = store.Close() }()

	acct, err := account.Get(t.Context(), store, 1)
	if err != nil {
		t.Fatalf("read the kept checkbook: %v", err)
	}
	reg, err := transaction.LoadRegister(t.Context(), store, acct)
	if err != nil {
		t.Fatalf("load the register: %v", err)
	}
	var found bool
	for _, e := range reg.Entries {
		if e.Payee == "After the backup" {
			found = true
		}
	}
	if !found {
		t.Error("the kept checkbook does not hold the transaction the restore took away")
	}
}

// TestAFailedRestoreLeavesTheCheckbookOpen is what restore-first buys, and it
// must not regress.
//
// The long, failure-prone step runs while the register is still open and
// serving, so a bad backup, a full disk, or an unwritable folder is refused with
// the checkbook still in front of the reader -- where there is no recovery path
// to write, and none to test.
func TestAFailedRestoreLeavesTheCheckbookOpen(t *testing.T) {
	h, _, backupPath := restorable(t)

	// The backup's pages are scribbled over while its header is left intact, so
	// it is still listed as one of ours and fails when it is read. That is the
	// shape of a damaged backup, and it is what the long step exists to catch.
	damage(t, backupPath)

	w := postFromPage(t, h, "/checkbook/restore", url.Values{
		"path":       {backupPath},
		"generation": {generationOf(t, h)},
	})
	if w.Code == http.StatusSeeOther {
		t.Fatal("a restore from a file that is not a checkbook reported success")
	}
	if !strings.Contains(w.Body.String(), "nothing about it has changed") {
		t.Errorf("the refusal does not say the checkbook is untouched:\n%s", w.Body.String())
	}

	// The whole point: the register is still there, with everything in it.
	body := get(t, h, "/accounts/1").Body.String()
	if !strings.Contains(body, "After the backup") {
		t.Error("the register lost records to a restore that failed")
	}
}

// damage scribbles over a database's pages, leaving its header alone, so it is
// still recognizably one of ours and no longer readable.
func damage(t *testing.T, path string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	rubbish := make([]byte, 8192)
	for i := range rubbish {
		rubbish[i] = 0x5a
	}
	if _, err := f.WriteAt(rubbish, 4096); err != nil {
		t.Fatalf("damage %s: %v", path, err)
	}
}

// TestStaleRestoreIsRefused is CO-3's shape applied to the checkbook, the same
// way TestStaleCloseIsRefused applies it to closing: a tab drawn before a swap
// must not replace a database it was never looking at.
func TestStaleRestoreIsRefused(t *testing.T) {
	h, _, backupPath := restorable(t)

	stale := generationOf(t, h)

	// Another window closes the checkbook and opens it again, which is a new
	// generation.
	if w := postFromPage(t, h, "/checkbook/close", url.Values{"generation": {stale}}); w.Code != http.StatusOK {
		t.Fatalf("close = %d, want 200", w.Code)
	}

	w := postFromPage(t, h, "/checkbook/restore", url.Values{
		"path": {backupPath}, "generation": {stale},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("stale restore = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "drawn for a different checkbook") {
		t.Error("the refusal does not say what happened")
	}
}

// TestRestoreRefusesABackupItDidNotOffer. The path arrives in a form, and a
// restore acts on the household's whole file: nothing is done to a path this
// program did not just list for itself.
func TestRestoreRefusesABackupItDidNotOffer(t *testing.T) {
	h, path, _ := restorable(t)

	elsewhere := filepath.Join(t.TempDir(), "somebody-elses.db")
	store, err := storage.Open(t.Context(), elsewhere)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_ = store.Close()

	for _, tt := range []struct {
		name string
		path string
	}{
		{"a file in another folder", elsewhere},
		{"nothing at all", ""},
		{"the checkbook itself", path},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := postFromPage(t, h, "/checkbook/restore", url.Values{
				"path": {tt.path}, "generation": {generationOf(t, h)},
			})
			if w.Code == http.StatusSeeOther {
				t.Fatal("the restore went ahead")
			}
			if !strings.Contains(get(t, h, "/accounts/1").Body.String(), "After the backup") {
				t.Error("the register changed though the restore was refused")
			}
		})
	}
}

// TestRestoreRefusesTheDemo. The sample household is held in memory: there is no
// file behind it for a restored copy to replace, and writing one into somebody's
// folder because they were looking at sample data would be worse than refusing.
func TestRestoreRefusesTheDemo(t *testing.T) {
	demo := open(t)
	seed(t, demo)
	h := newServer(t, web.Options{Store: demo, Open: memoryOpener(t)})

	// Not offered in the frame ...
	if strings.Contains(get(t, h, "/accounts/1").Body.String(), `href="/checkbook/restore"`) {
		t.Error("the sidebar offers to restore over the sample household")
	}
	// ... and the page says why rather than showing a press that would fail.
	body := get(t, h, "/checkbook/restore").Body.String()
	if !strings.Contains(body, "sample household is open") {
		t.Errorf("the restore page does not say why the press is withheld:\n%s", body)
	}
	if strings.Contains(body, "Restore this backup") {
		t.Error("the restore page offers a press that could only be refused")
	}
}

// TestRestoreIsWithheldWithoutAnOpener: the one press has to reopen, so a build
// with no Opener cannot make it. The route is not registered and the page says
// what can be done instead.
func TestRestoreIsWithheldWithoutAnOpener(t *testing.T) {
	store, path := openFile(t)
	seed(t, store)
	h := newServer(t, web.Options{Store: store, CheckbookPath: path})

	body := get(t, h, "/checkbook/restore").Body.String()
	if strings.Contains(body, "Restore this backup") {
		t.Error("a build with no opener offers the one-press restore")
	}
	if !strings.Contains(body, "This build cannot open a checkbook by itself") {
		t.Error("the page does not say why")
	}

	// 405 rather than 404: the address falls to the catch-all, which is a GET
	// route. What matters is that the handler does not exist.
	w := postFromPage(t, h, "/checkbook/restore", url.Values{"path": {"anything.db"}})
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /checkbook/restore without an opener = %d, want 405", w.Code)
	}
}

// TestTheCopyRestoreStillWorks. The older restore is not replaced by the new
// one: it needs no Opener, it replaces nothing, and it is the answer when the
// file you want back is not the one you are working in.
func TestTheCopyRestoreStillWorks(t *testing.T) {
	h, path, backupPath := restorable(t)

	dest := filepath.Join(filepath.Dir(path), "a-copy.db")
	w := postFromPage(t, h, "/checkbook/restore/copy", url.Values{
		"source": {backupPath}, "dest": {dest}, "return": {"/checkbook"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("copy restore = %d, want 303: %s", w.Code, w.Body.String())
	}
	if id, err := storage.ApplicationID(dest); err != nil || id != storage.AppID {
		t.Errorf("the copy's application id = %d, %v; want a checkbook's", id, err)
	}
	// And the checkbook it did not touch is still open, with everything in it.
	if !strings.Contains(get(t, h, "/accounts/1").Body.String(), "After the backup") {
		t.Error("restoring to a copy changed the checkbook that was open")
	}
}

// TestRestoreUnderLoad is the load-bearing test, and it is meant to be run with
// -race: readers hammering the register while another window restores.
//
// Nothing may panic, every answer must be one of this program's pages, and at
// the end there must be exactly one file at the checkbook's name -- and it must
// open.
func TestRestoreUnderLoad(t *testing.T) {
	h, path, backupPath := restorable(t)

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
				for _, address := range []string{"/", "/accounts/1", "/checkbook/restore"} {
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

	for range 3 {
		w := postFromPage(t, h, "/checkbook/restore", url.Values{
			"path": {backupPath}, "generation": {generationOf(t, h)},
		})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusConflict {
			t.Errorf("restore = %d: %s", w.Code, firstLine(w.Body.String()))
		}
	}

	close(stop)
	readers.Wait()

	mu.Lock()
	if len(bad) > 0 {
		t.Errorf("%d answers were not one of ours; the first is %s", len(bad), bad[0])
	}
	mu.Unlock()

	// Exactly one file at the checkbook's name, and it opens.
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		t.Fatalf("the checkbook is not at its own name: %v", err)
	}
	if code := get(t, h, "/accounts/1").Code; code != http.StatusOK {
		t.Errorf("the register after the restores = %d, want 200", code)
	}
}

// TestTwoRestoresAtOnce. ctl is held across the close, the renames and the open
// as one critical section, so a second press waits rather than interleaving with
// the first -- and the second is refused as stale, because the first changed the
// checkbook it was drawn for.
func TestTwoRestoresAtOnce(t *testing.T) {
	h, path, backupPath := restorable(t)

	gen := generationOf(t, h)
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := postFromPage(t, h, "/checkbook/restore", url.Values{
				"path": {backupPath}, "generation": {gen},
			})
			results <- w.Code
		}()
	}
	wg.Wait()
	close(results)

	var swapped, refused int
	for code := range results {
		switch code {
		case http.StatusSeeOther:
			swapped++
		case http.StatusConflict:
			refused++
		default:
			t.Errorf("a concurrent restore answered %d", code)
		}
	}
	if swapped != 1 || refused != 1 {
		t.Errorf("%d restores went ahead and %d were refused; want one of each", swapped, refused)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the checkbook is not at its own name: %v", err)
	}
	if code := get(t, h, "/accounts/1").Code; code != http.StatusOK {
		t.Errorf("the register after two presses = %d, want 200", code)
	}
}

// TestAnOpenDuringTheSwapCannotCreateAnEmptyCheckbook is the race that justifies
// holding ctl across steps 3 to 6.
//
// Between the two renames there is no file at the checkbook's name. A
// POST /checkbook/open for that name in that window would have storage.Open
// create an empty checkbook, migrate it, and adopt it -- and then the second
// rename would put a file over it, leaving a live pool on an unlinked inode and
// a household typing into nothing. handleOpen takes ctl for its whole body, so
// it cannot run inside that window.
func TestAnOpenDuringTheSwapCannotCreateAnEmptyCheckbook(t *testing.T) {
	h, path, backupPath := restorable(t)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		postFromPage(t, h, "/checkbook/restore", url.Values{
			"path": {backupPath}, "generation": {generationOf(t, h)},
		})
	}()
	go func() {
		defer wg.Done()
		postFromPage(t, h, "/checkbook/open", url.Values{"path": {path}})
	}()
	wg.Wait()

	// Whatever order they ran in, there is one checkbook at the name and it has
	// the backup's records in it -- never an empty one storage.Open created in
	// the window.
	store, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("the checkbook does not open: %v", err)
	}
	defer func() { _ = store.Close() }()

	accounts, err := account.List(t.Context(), store)
	if err != nil {
		t.Fatalf("list accounts: %v", err)
	}
	if len(accounts) == 0 {
		t.Fatal("the checkbook is empty: an open in the swap's window created one")
	}
}
