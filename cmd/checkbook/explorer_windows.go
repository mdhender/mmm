// Copyright (c) 2026 Michael D Henderson.

//go:build windows

package main

import (
	"slices"
	"strings"
	"syscall"
	"unsafe"
)

// explorerExe is the shell that launches a program when its icon is
// double-clicked, or opened from the Start menu, the taskbar, or a shortcut.
const explorerExe = "explorer.exe"

// startedByExplorer reports whether this process was launched from the Windows
// shell rather than typed at a command line.
//
// It is what decides whether a failure has to hold the console window open. A
// console allocated for a double-click closes the instant the process exits,
// taking any message with it; a console belonging to PowerShell or cmd does not,
// and stopping there to ask for Enter would only be in the way.
//
// The question is answered by asking whether the parent process is explorer.exe.
// Windows keeps a process's parent id even after the parent exits -- there is no
// reparenting as there is on Unix -- so the id is read from the same snapshot
// that supplies the names, which at least makes the two consistent with each
// other.
//
// It is conservative: every failure returns false, which costs a message the
// reader may not see, rather than a program that stops for input nobody is there
// to give.
//
// Two limits worth knowing, neither of which is worth more machinery than this:
//
//   - A parent that has already exited leaves an id Windows may have handed to
//     something else. Usually the id matches nothing in the snapshot and the
//     answer is false; the wrong answer needs the reused id to belong to
//     explorer.exe in particular, and costs only a held console.
//   - Launching by any other route reads as a command line: a third-party file
//     manager, a double-clicked .bat that runs this program, or "Run as
//     administrator", where the parent becomes the elevation service. The
//     message is then lost the way it is today.
func startedByExplorer() bool {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(snapshot)

	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return false
	}

	// One pass over one snapshot: the parent's id and the shells it might name
	// are read from the same instant, and only the handful of explorer.exe ids
	// are kept rather than a table of every process on the machine.
	self := uint32(syscall.Getpid())
	var (
		parent      uint32
		foundSelf   bool
		explorerIDs []uint32
	)
	for {
		if entry.ProcessID == self {
			parent, foundSelf = entry.ParentProcessID, true
		}
		// Windows compares filenames without regard to case, and the snapshot
		// reports whatever case is on disk.
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), explorerExe) {
			explorerIDs = append(explorerIDs, entry.ProcessID)
		}
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			break // end of the snapshot, or it could not be read further
		}
	}

	return foundSelf && slices.Contains(explorerIDs, parent)
}
