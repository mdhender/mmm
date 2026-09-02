// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestShouldHoldConsole pins the remaining conditions. The caller has already
// established that the program failed to start and that no browser window
// carried the message; only a double-click with a real console should block.
func TestShouldHoldConsole(t *testing.T) {
	regular, err := os.Create(filepath.Join(t.TempDir(), "not-a-console"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer regular.Close()

	console, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer console.Close()
	if info, err := console.Stat(); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a character device here", os.DevNull)
	}

	tests := []struct {
		name     string
		explorer bool
		stdin    *os.File
		want     bool
	}{
		{"double-clicked", true, console, true},
		// Someone who typed the command keeps their window and their message,
		// and should get their prompt back rather than a question.
		{"run from a command line", false, console, false},
		// A pipe or a file has nobody to press anything, and waiting would hang
		// whatever is driving the program.
		{"double-clicked, redirected input", true, regular, false},
		{"command line, redirected input", false, regular, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldHoldConsole(tt.explorer, tt.stdin); got != tt.want {
				t.Errorf("shouldHoldConsole(%v) = %v, want %v", tt.explorer, got, tt.want)
			}
		})
	}
}

// TestStartedByExplorerOffWindows: the console hold is a Windows problem, and
// everywhere else the answer is a constant. This is what keeps a Unix shell or a
// macOS Terminal from ever waiting on input.
func TestStartedByExplorerOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows implementation asks the operating system")
	}
	if startedByExplorer() {
		t.Errorf("startedByExplorer() = true on %s, want false", runtime.GOOS)
	}
}

func TestHoldConsolePromptsAndReturns(t *testing.T) {
	var out strings.Builder
	holdConsole(&out, strings.NewReader("\n"))

	if !strings.Contains(out.String(), "Press Enter") {
		t.Errorf("no prompt was shown: %q", out.String())
	}
}

// TestHoldConsoleReturnsOnClosedInput: a console that is somehow at end of input
// must not leave the program hanging forever.
func TestHoldConsoleReturnsOnClosedInput(t *testing.T) {
	var out strings.Builder
	holdConsole(&out, strings.NewReader(""))
}
