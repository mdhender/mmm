// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShouldHoldConsole pins the three conditions. Only Windows started with a
// console and a reader expecting a browser should ever block on input.
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
		name  string
		goos  string
		open  bool
		stdin *os.File
		want  bool
	}{
		{"windows console", "windows", true, console, true},
		// A Terminal window opened by double-clicking on macOS stays open on a
		// failing exit, and a Unix shell never closes on its own.
		{"macos", "darwin", true, console, false},
		{"linux", "linux", true, console, false},
		// -open=false means something is driving the program rather than
		// someone, and waiting for input would hang it.
		{"windows, browser suppressed", "windows", false, console, false},
		// A pipe or a file has nobody to press anything.
		{"windows, redirected input", "windows", true, regular, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldHoldConsole(tt.goos, tt.open, tt.stdin); got != tt.want {
				t.Errorf("shouldHoldConsole(%q, %v) = %v, want %v", tt.goos, tt.open, got, tt.want)
			}
		})
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
