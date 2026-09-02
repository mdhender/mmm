// Copyright (c) 2026 Michael D Henderson.

// Command checkbook serves the household register in the browser.
//
// It is one native executable with no installer, no database server, and no
// background service (SPECIFICATION.md PL-1, PL-2). Starting it opens an HTTP
// listener on the loopback interface and points the machine's normal browser at
// it (PL-4); stopping it with Ctrl+C closes the database and leaves nothing
// running.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mdhender/mmm"
	"github.com/mdhender/mmm/internal/cerrs"
	"github.com/mdhender/mmm/internal/storage"
	"github.com/mdhender/mmm/internal/web"
)

// errReported ends the program with a failing status when the reason has already
// been shown to the user -- printed to the terminal and served as a page -- so
// main does not repeat it.
const errReported = cerrs.Error("already reported")

// DefaultPort is fixed rather than left to the system so that the register has a
// stable address.
//
// An address that changes every run cannot be bookmarked, which leaves someone
// who has closed the browser with no way back to a program that is still
// running -- and so starting it again, repeatedly. A fixed port also makes the
// second start fail to bind, which is a truthful way of saying the checkbook is
// already open without going looking for it.
//
// Pass -port 0 to ask the system for a free one instead.
const DefaultPort = 8842

// DemoPort is where -demo listens when -port was not given.
//
// The demo is what somebody reaches for while their own checkbook is open: to
// show another person what a register looks like, or to try an interaction
// without touching real records. Sharing DefaultPort would make that a choice
// between the two, since the second start cannot bind. It sits next to the
// register's port so it is as easy to remember, and it is fixed for the same
// reason DefaultPort is.
//
// An explicit -port always wins, including one that names DefaultPort.
const DemoPort = 8843

// demoName names the in-memory database used by -demo; demoDatabase is what a
// Store opened under that name reports as its path, and what the UI shows.
const (
	demoName     = "checkbook-demo"
	demoDatabase = ":memory:" + demoName
)

// Flags are package-level so that main can consult them after run returns --
// whether to hold the console open depends on -open. See console.go.
var (
	dbPath      = flag.String("db", "checkbook.db", "path to the checkbook database")
	host        = flag.String("host", "127.0.0.1", "loopback address to listen on")
	port        = flag.Int("port", DefaultPort, "port to listen on; 0 asks the system for a free one, and -demo uses "+strconv.Itoa(DemoPort)+" unless this is given")
	openBrowser = flag.Bool("open", true, "open the register in the default browser")
	demo        = flag.Bool("demo", false, "serve a sample household from memory, touching no files")
	showVersion = flag.Bool("version", false, "print the version and exit")
)

func main() {
	flag.Parse()
	listenPort = portFor(*port, *demo, portWasGiven(flag.CommandLine))

	browserOpened, err := run()
	if err == nil {
		return
	}
	if !errors.Is(err, errReported) {
		fmt.Fprintf(os.Stderr, "checkbook: %v\n", err)
	}
	// The console is the last resort, not the first. When a browser window was
	// opened it is already carrying the explanation -- a failure to open the
	// database is served as a page -- and there is nothing here the reader needs
	// to stop and read.
	if !browserOpened {
		holdConsoleOnExit()
	}
	os.Exit(1)
}

func run() (browserOpened bool, err error) {
	version := mmm.Version()
	if *showVersion {
		fmt.Println(version.String())
		return false, nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Refuse anything but loopback rather than trusting the flag. The register is
	// unauthenticated by design (PL-7), which is only safe because it is
	// unreachable from off the machine (PL-4).
	if err := requireLoopback(*host); err != nil {
		return false, err
	}

	// The listener is opened before the database so that a port already in use
	// fails without having touched the records.
	addr := net.JoinHostPort(*host, fmt.Sprint(listenPort))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// Much the most likely cause is the household's own checkbook, already
		// open and forgotten. Say where it is rather than reporting a bind
		// failure they can do nothing with.
		if listenPort != 0 && isAddrInUse(err) {
			return false, portInUse(os.Stderr, *host, listenPort)
		}
		return false, fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// One more cancel on top of the signal's, handed to the browser's Quit. The
	// select below then fires for both Ctrl+C and the button, and everything
	// after it -- the shutdown, the drain, the close -- happens in exactly the
	// same order either way.
	ctx, quit := context.WithCancel(ctx)
	defer quit()

	// A database that will not open is not a reason to exit in silence. On a
	// desktop the program is started by double-clicking it and the terminal may
	// not be visible at all, so the failure is served as a page in the browser
	// that was going to be opened anyway. It is printed here too, for whoever
	// does have a terminal.
	var handler http.Handler
	store, storeErr := openStore(ctx, *dbPath, *demo)
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "checkbook: %v\n", storeErr)
		handler, err = web.NewProblem(
			web.DescribeOpenError(storeErr, databaseName(*dbPath, *demo)),
			version.Short())
		if err != nil {
			return false, err
		}
	} else {
		// Pool.Close blocks until every borrowed connection is returned, so this
		// must run after the server has finished its in-flight requests. The
		// shutdown below does that before returning.
		//
		// It closes the server's checkbook rather than this store, because the
		// store the program started with may not be the one it ends with: the
		// register can be closed and another opened from the browser.
		ui, err := web.New(web.Options{
			Store:   store,
			Open:    browserOpener(log),
			Quit:    quit,
			Restart: restartHint(flag.CommandLine),
			Version: version.Short(),
			Log:     log,
		})
		if err != nil {
			store.Close()
			return false, err
		}
		defer ui.Close()
		handler = ui
	}

	srv := &http.Server{
		Handler: handler,
		// A local browser is the only client, so these are generous. They exist
		// to keep a wedged connection from holding a pool slot forever.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	url := "http://" + listener.Addr().String() + "/"
	fmt.Printf("checkbook %s\n", version.Short())
	fmt.Printf("database:  %s\n", databaseName(*dbPath, *demo))
	switch {
	case storeErr != nil:
		fmt.Printf("           NOT OPENED -- the address below explains why\n")
	case *demo:
		fmt.Printf("           sample data, held in memory; nothing is written to disk\n")
	}
	fmt.Printf("register:  %s\n", url)
	fmt.Printf("press Ctrl+C to stop, or use Quit in the browser\n")

	// Whether this succeeded decides where a later failure has to be reported.
	// A browser showing the problem page has already told the reader what
	// happened; a console is only needed when nothing else could.
	if *openBrowser {
		if err := openInBrowser(url); err != nil {
			// Not fatal: the address is printed above and can be pasted in.
			log.Warn("could not open a browser", "url", url, "err", err)
		} else {
			browserOpened = true
		}
	}

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		// Note that Shutdown does not run on this path, so a handler may still
		// be in flight when the deferred close fires. It drains rather than
		// hangs -- Pool.Close interrupts the connections it is waiting on --
		// but the ordering guarantee below does not hold here.
		return browserOpened, err
	case <-ctx.Done():
		fmt.Println("\nstopping")
	}

	// Shutdown waits for handlers to return, which is what returns their
	// database connections to the pool and lets the deferred Close finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return browserOpened, fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; err != nil {
		return browserOpened, err
	}
	// The program ran, but it never opened the records it was asked for. Say so
	// in the exit status; the reason is already on screen.
	if storeErr != nil {
		return browserOpened, errReported
	}
	return browserOpened, nil
}

// databaseName is what the program calls the database in its own output. It is
// needed before the store exists, and when the store could not be built at all.
func databaseName(path string, demo bool) string {
	if demo {
		return demoDatabase
	}
	return path
}

// browserOpener lets the browser open another checkbook without restarting the
// program.
//
// It is a function passed to web rather than something web does for itself,
// because seeding the sample household exists only in this command and the
// wording ST-6 asks for when a folder is missing is openStore's. The path is
// made absolute here: the program may have been started from anywhere, and a
// relative path typed into a browser box is a path relative to a working
// directory the reader cannot see.
func browserOpener(log *slog.Logger) web.Opener {
	return func(ctx context.Context, req web.OpenRequest) (*storage.Store, error) {
		path := req.Path
		if !req.Demo {
			abs, err := filepath.Abs(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			path = abs
		}
		log.Info("opening a checkbook", "path", path, "demo", req.Demo, "readonly", req.ReadOnly)
		if req.Demo {
			return openStore(ctx, path, true)
		}
		if req.ReadOnly {
			// A backup opened read-write is no longer a backup: Open migrates on
			// open, so merely looking at an older copy would rewrite the thing
			// that was kept.
			return storage.OpenReadOnly(ctx, path)
		}
		return openStore(ctx, path, false)
	}
}

// openStore opens the database, or builds the in-memory sample when demo is set.
func openStore(ctx context.Context, path string, demo bool) (*storage.Store, error) {
	if demo {
		store, err := storage.OpenMemory(ctx, demoName)
		if err != nil {
			return nil, fmt.Errorf("open sample database: %w", err)
		}
		if err := seedDemo(ctx, store); err != nil {
			store.Close()
			return nil, err
		}
		return store, nil
	}

	store, err := storage.Open(ctx, path)
	if err != nil {
		if errors.Is(err, storage.ErrMissingDirectory) {
			// ST-6: a mistyped path is reported, never built. Say which directory
			// is missing and what to do about it (RG-4).
			return nil, fmt.Errorf("%w\n\tthe directory %q does not exist; create it yourself, "+
				"or pass -db with a path inside a directory that does", err, filepath.Dir(path))
		}
		return nil, err
	}
	return store, nil
}

// requireLoopback reports an error unless host names the loopback interface.
//
// Only literal IP addresses and the name "localhost" are accepted. Resolving an
// arbitrary name could mean a DNS query, and the program is required to run
// without a network (PL-3).
func requireLoopback(host string) error {
	if host == "" {
		return errors.New("-host must be a loopback address such as 127.0.0.1")
	}

	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("-host %s is not a loopback address; the register is served only to this machine", host)
		}
		return nil
	}

	if host != "localhost" {
		return fmt.Errorf("-host %q is not a loopback address; use 127.0.0.1, ::1, or localhost", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve localhost: %w", err)
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("localhost resolves to %s, which is not a loopback address; use 127.0.0.1", ip)
		}
	}
	return nil
}

// openInBrowser asks the operating system to open url in whatever browser the
// user already uses. No browser is bundled and none is required to be installed
// (PL-4).
func openInBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// rundll32 rather than "cmd /c start", which treats & in a URL as a
		// command separator.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
