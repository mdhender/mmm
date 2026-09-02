// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"strconv"
	"syscall"
)

// listenPort is the port run listens on: what -port asked for, or the demo's own
// port when -demo was given without one. main sets it once, after flag.Parse.
var listenPort int

// portWasGiven reports whether -port appeared on the command line.
//
// Visit walks only the flags that were set, which is the difference that matters
// here: -port 8842 must be honored even though it names the default, and must
// not be quietly moved because -demo is also present.
func portWasGiven(fs *flag.FlagSet) bool {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			given = true
		}
	})
	return given
}

// portFor chooses the port to listen on.
//
// The demo moves out of the register's way so that both can run at once: it is
// what somebody opens while their own checkbook is already open, and two copies
// cannot hold one port. An explicit -port always wins, whatever it names.
func portFor(port int, demo, portGiven bool) int {
	if demo && !portGiven {
		return DemoPort
	}
	return port
}

// windowsAddrInUse is Windows' WSAEADDRINUSE.
//
// Windows does not use the POSIX value for this error, and syscall.WSAEADDRINUSE
// exists only in Windows builds. Writing the number here keeps the check in one
// file that builds everywhere; on other platforms nothing ever returns it, so it
// cannot match by accident.
const windowsAddrInUse = syscall.Errno(10048)

// isAddrInUse reports whether a listen failed because something already holds
// the address.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, windowsAddrInUse)
}

// portInUse explains that the port is taken, and where the checkbook probably
// is.
//
// It does not claim to know what is listening: nothing has been asked, and
// asserting that the register is there when some other program holds the port
// would be worse than saying nothing. Both possibilities are given, each with
// what to do about it (SPECIFICATION.md RG-4).
func portInUse(out io.Writer, host string, port int) error {
	address := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/"

	fmt.Fprintf(out, "checkbook: port %d is already in use\n\n", port)
	fmt.Fprintf(out, "If your checkbook is already open, this is where it is:\n")
	fmt.Fprintf(out, "    %s\n\n", address)
	// The port suggested skips the demo's, which is the one other port this
	// program is likely to be holding: advice that lands on a running demo is
	// advice that fails twice.
	next := port + 1
	if next == DemoPort {
		next++
	}

	fmt.Fprintf(out, "Open that address, or close the copy that is running before starting\n")
	fmt.Fprintf(out, "another. If something else on this machine is using port %d, start\n", port)
	fmt.Fprintf(out, "this copy on a different one:\n")
	fmt.Fprintf(out, "    -port %d\n", next)

	return errReported
}
