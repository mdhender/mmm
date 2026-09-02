// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"

	"github.com/mdhender/mmm/internal/web"
)

// restartHint composes the line that starts the program again, for the page the
// reader is left with after quitting.
//
// It is captured at startup, before anything can change directory, because both
// halves of the answer -- which executable, and where it was run from -- are
// only true then.
//
// Only the flags flag.Visit reports as actually set are repeated. Printing every
// flag with its default would turn a two-word command into a paragraph, and the
// defaults are defaults. -db is the exception that is made absolute: it is the
// one flag whose meaning depends on the directory, and it is the one worth being
// sure of.
func restartHint(fs *flag.FlagSet) web.RestartHint {
	hint := web.RestartHint{}
	if cwd, err := os.Getwd(); err == nil {
		hint.Directory = cwd
	}

	parts := []string{program()}
	fs.Visit(func(f *flag.Flag) {
		value := f.Value.String()
		if f.Name == "db" {
			if abs, err := filepath.Abs(value); err == nil {
				value = abs
			}
		}
		// A bool flag is written as its own name. "-demo true" is not what
		// anybody types, and on some shells it is not even accepted.
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			if value == "true" {
				parts = append(parts, "-"+f.Name)
			} else {
				parts = append(parts, "-"+f.Name+"="+value)
			}
			return
		}
		parts = append(parts, "-"+f.Name, quoteIfNeeded(value))
	})

	hint.Command = strings.Join(parts, " ")
	return hint
}

// program is how this build is started.
//
// Under "go run" the executable is a file in the build cache with a name nobody
// could type and which will not be there tomorrow, so the answer is the source
// path instead. That is also the honest answer for a developer, who started it
// that way and will start it that way again.
func program() string {
	exe, err := os.Executable()
	if err != nil {
		return "go run ./cmd/checkbook"
	}
	if isBuildCache(exe) {
		return "go run ./cmd/checkbook"
	}
	return quoteIfNeeded(exe)
}

// isBuildCache reports whether path looks like something "go run" built.
//
// Go puts it under a "go-build" directory inside the temporary directory. This
// is a heuristic and it is allowed to be: getting it wrong prints a command that
// is merely less convenient, never one that is wrong to run.
func isBuildCache(path string) bool {
	if strings.Contains(path, string(filepath.Separator)+"go-build") {
		return true
	}
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(tmp, real)
	return err == nil && !strings.HasPrefix(rel, "..")
}

// quoteIfNeeded wraps a value in double quotes when it holds a space, which is
// what a household folder name most often holds.
func quoteIfNeeded(v string) string {
	if v == "" {
		return `""`
	}
	if !strings.ContainsAny(v, " \t") {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}
