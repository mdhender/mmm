// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestartHintRepeatsOnlyTheFlagsThatWereGiven. Printing every flag with its
// default would turn a two-word command into a paragraph, and the defaults are
// defaults.
func TestRestartHintRepeatsOnlyTheFlagsThatWereGiven(t *testing.T) {
	fs := flag.NewFlagSet("checkbook", flag.ContinueOnError)
	fs.String("db", "checkbook.db", "")
	fs.Int("port", DefaultPort, "")
	fs.Bool("demo", false, "")
	fs.Bool("open", true, "")

	if err := fs.Parse([]string{"-port", "9000", "-demo"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	hint := restartHint(fs)
	if !strings.Contains(hint.Command, "-port 9000") {
		t.Errorf("the hint does not repeat -port: %q", hint.Command)
	}
	// A bool is written as its own name: "-demo true" is not what anybody types.
	if !strings.Contains(hint.Command, " -demo") || strings.Contains(hint.Command, "-demo true") {
		t.Errorf("the hint writes -demo as a value: %q", hint.Command)
	}
	for _, unwanted := range []string{"-db", "-open"} {
		if strings.Contains(hint.Command, unwanted) {
			t.Errorf("the hint repeats %s, which was never given: %q", unwanted, hint.Command)
		}
	}
	if hint.Directory == "" {
		t.Error("the hint does not say where to type it")
	}
}

// TestRestartHintMakesTheDatabaseAbsolute. It is the one flag whose meaning
// depends on the directory, and the one worth being sure of.
func TestRestartHintMakesTheDatabaseAbsolute(t *testing.T) {
	fs := flag.NewFlagSet("checkbook", flag.ContinueOnError)
	fs.String("db", "checkbook.db", "")
	if err := fs.Parse([]string{"-db", "records/checkbook.db"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	hint := restartHint(fs)
	want, err := filepath.Abs("records/checkbook.db")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if !strings.Contains(hint.Command, want) {
		t.Errorf("the hint carries a relative database path: %q", hint.Command)
	}
}

// TestRestartHintNamesTheProgram. Under "go run" the executable is a file in the
// build cache with a name nobody could type and which will not be there
// tomorrow, so the source path is the honest answer. The tests themselves run
// from a cached binary, so this is the case being exercised.
func TestRestartHintNamesTheProgram(t *testing.T) {
	fs := flag.NewFlagSet("checkbook", flag.ContinueOnError)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	hint := restartHint(fs)
	if hint.Command == "" {
		t.Fatal("the hint names no program at all")
	}
	if strings.Contains(hint.Command, "-") {
		t.Errorf("the hint carries flags that were never given: %q", hint.Command)
	}
}

// TestQuoteIfNeeded: a household folder name most often holds a space.
func TestQuoteIfNeeded(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"checkbook.db", "checkbook.db"},
		{"", `""`},
		{"/Users/pat/My Records/checkbook.db", `"/Users/pat/My Records/checkbook.db"`},
	} {
		if got := quoteIfNeeded(tt.in); got != tt.want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
