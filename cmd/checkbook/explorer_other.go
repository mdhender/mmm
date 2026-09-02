// Copyright (c) 2026 Michael D Henderson.

//go:build !windows

package main

// startedByExplorer is always false away from Windows.
//
// Nothing else here allocates a console that closes when the program exits: a
// Terminal window opened by double-clicking on macOS stays open on a failing
// exit, and a Unix shell never closes on its own. There is nothing to hold, so
// there is nothing to detect.
func startedByExplorer() bool { return false }
