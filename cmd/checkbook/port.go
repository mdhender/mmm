// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"syscall"
)

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
	fmt.Fprintf(out, "Open that address, or close the copy that is running before starting\n")
	fmt.Fprintf(out, "another. If something else on this machine is using port %d, start\n", port)
	fmt.Fprintf(out, "this copy on a different one:\n")
	fmt.Fprintf(out, "    -port %d\n", port+1)

	return errReported
}
