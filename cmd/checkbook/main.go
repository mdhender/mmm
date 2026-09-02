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
	port        = flag.Int("port", 0, "port to listen on; 0 asks the system for a free one")
	openBrowser = flag.Bool("open", true, "open the register in the default browser")
	demo        = flag.Bool("demo", false, "serve a sample household from memory, touching no files")
	showVersion = flag.Bool("version", false, "print the version and exit")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		if !errors.Is(err, errReported) {
			fmt.Fprintf(os.Stderr, "checkbook: %v\n", err)
		}
		holdConsoleOnExit(*openBrowser)
		os.Exit(1)
	}
}

func run() error {
	version := mmm.Version()
	if *showVersion {
		fmt.Println(version.String())
		return nil
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Refuse anything but loopback rather than trusting the flag. The register is
	// unauthenticated by design (PL-7), which is only safe because it is
	// unreachable from off the machine (PL-4).
	if err := requireLoopback(*host); err != nil {
		return err
	}

	// The listener is opened before the database so that a port already in use
	// fails without having touched the records.
	addr := net.JoinHostPort(*host, fmt.Sprint(*port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer listener.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A database that will not open is not a reason to exit in silence. On a
	// desktop the program is started by double-clicking it and the terminal may
	// not be visible at all, so the failure is served as a page in the browser
	// that was going to be opened anyway. It is printed here too, for whoever
	// does have a terminal.
	var handler http.Handler
	store, storeErr := openStore(ctx, *dbPath, *demo)
	if storeErr == nil && !*demo {
		// Claim the database now that it is known to open and the address is
		// known. A second copy of the program on the same file is the case this
		// guards: two registers over one database, each unaware of the other's
		// writes, is precisely the silent loss CO-3 exists to prevent.
		//
		// -demo is exempt. Its database is held in memory and private to the
		// process, so two of them share nothing.
		resolved, err := databasePath(*dbPath)
		if err != nil {
			store.Close()
			return err
		}
		held, other, err := acquireLock(resolved, "http://"+listener.Addr().String()+"/", os.Getpid())
		if err != nil {
			store.Close()
			return err
		}
		if other != nil {
			store.Close()
			return alreadyRunning(os.Stderr, *other, resolved, *openBrowser, log)
		}
		defer held.Release()
	}
	if storeErr != nil {
		fmt.Fprintf(os.Stderr, "checkbook: %v\n", storeErr)
		handler, err = web.NewProblem(
			web.DescribeOpenError(storeErr, databaseName(*dbPath, *demo)),
			version.Short())
		if err != nil {
			return err
		}
	} else {
		// Pool.Close blocks until every borrowed connection is returned, so this
		// must run after the server has finished its in-flight requests. The
		// shutdown below does that before returning.
		defer store.Close()

		served, err := databasePath(*dbPath)
		if err != nil {
			return err
		}
		if *demo {
			served = demoDatabase
		}
		if handler, err = web.New(store, version.Short(), served, log); err != nil {
			return err
		}
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
	fmt.Printf("press Ctrl+C to stop\n")

	if *openBrowser {
		if err := openInBrowser(url); err != nil {
			// Not fatal: the address is printed above and can be pasted in.
			log.Warn("could not open a browser", "url", url, "err", err)
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
		return err
	case <-ctx.Done():
		fmt.Println("\nstopping")
	}

	// Shutdown waits for handlers to return, which is what returns their
	// database connections to the pool and lets the deferred Close finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; err != nil {
		return err
	}
	// The program ran, but it never opened the records it was asked for. Say so
	// in the exit status; the reason is already on screen.
	if storeErr != nil {
		return errReported
	}
	return nil
}

// databaseName is what the program calls the database in its own output. It is
// needed before the store exists, and when the store could not be built at all.
func databaseName(path string, demo bool) string {
	if demo {
		return demoDatabase
	}
	return path
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
