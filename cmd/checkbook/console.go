// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// holdConsole keeps a console window open until the reader presses Enter.
//
// The program is meant to be started by double-clicking it, and on Windows that
// allocates a console window that closes the instant the process exits. An error
// message printed into a window that vanishes has not been delivered.
//
// This is the last resort and not the usual path. A program that starts opens a
// browser, and a database that will not open is reported as a page in that
// browser. Only a failure early enough that there is no listener -- and so no
// page and no window -- has nowhere else to go.
func holdConsole(out io.Writer, in io.Reader) {
	fmt.Fprintln(out, "\nPress Enter to close this window.")
	// One line, or end of input. The result does not matter; the point is to
	// wait.
	_, _ = bufio.NewReader(in).ReadString('\n')
}

// shouldHoldConsole reports whether an exiting error needs the window held open.
//
// The caller has already established the part that matters: the program could
// not start, and no browser window was opened to say so. Two conditions remain:
//
//   - The program was launched from the Windows shell, which allocated a console
//     that closes the instant the process exits. Someone who typed the command
//     keeps their window and their message, and should get their prompt back
//     rather than a question. Away from Windows this is always false.
//   - Standard input is a console. When it is a pipe or a file there is nobody
//     to press anything, and waiting would hang whatever is driving the program.
//     Belt and braces after the first condition, and cheap.
func shouldHoldConsole(startedByExplorer bool, stdin *os.File) bool {
	if !startedByExplorer {
		return false
	}
	info, err := stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// holdConsoleOnExit applies shouldHoldConsole to the real process. Call it only
// when the program is exiting with an error that no browser window carried.
func holdConsoleOnExit() {
	if shouldHoldConsole(startedByExplorer(), os.Stdin) {
		holdConsole(os.Stdout, os.Stdin)
	}
}
