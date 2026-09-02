// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"runtime"
)

// holdConsole keeps a console window open until the reader presses Enter.
//
// The program is meant to be started by double-clicking it, and on Windows that
// allocates a console window that closes the instant the process exits. An error
// message printed into a window that vanishes has not been delivered. There is
// not much else to be done about it: the failures this covers happen before
// there is a listener, so they cannot be shown in the browser the way a database
// problem is.
func holdConsole(out io.Writer, in io.Reader) {
	fmt.Fprintln(out, "\nPress Enter to close this window.")
	// One line, or end of input. The result does not matter; the point is to
	// wait.
	_, _ = bufio.NewReader(in).ReadString('\n')
}

// shouldHoldConsole reports whether an exiting error needs the window held open.
//
// Three conditions, all of which have to hold:
//
//   - Windows. A macOS Terminal window opened by double-clicking stays open on a
//     failing exit, and a Unix shell never closes on its own.
//   - The reader expected a browser to open. Passing -open=false means something
//     is driving the program rather than someone, and blocking on input would
//     hang it.
//   - Standard input is a console. When it is a pipe or a file there is nobody
//     to press anything.
func shouldHoldConsole(goos string, openBrowser bool, stdin *os.File) bool {
	if goos != "windows" || !openBrowser {
		return false
	}
	info, err := stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// holdConsoleOnExit applies shouldHoldConsole to the real process.
func holdConsoleOnExit(openBrowser bool) {
	if shouldHoldConsole(runtime.GOOS, openBrowser, os.Stdin) {
		holdConsole(os.Stdout, os.Stdin)
	}
}
