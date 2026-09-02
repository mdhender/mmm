// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"flag"
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestPortForMovesTheDemoOutOfTheWay: the demo is what somebody opens while
// their own checkbook is already open, and two copies cannot hold one port.
func TestPortForMovesTheDemoOutOfTheWay(t *testing.T) {
	if DemoPort == DefaultPort {
		t.Fatal("the demo listens where the register does, so the two cannot run at once")
	}

	for _, tt := range []struct {
		name      string
		port      int
		demo      bool
		portGiven bool
		want      int
	}{
		{"the register", DefaultPort, false, false, DefaultPort},
		{"the demo", DefaultPort, true, false, DemoPort},
		{"the demo on a port that was asked for", 9001, true, true, 9001},
		{"the demo told to use the register's port", DefaultPort, true, true, DefaultPort},
		{"the demo asking the system for a free port", 0, true, true, 0},
		{"the register on a port that was asked for", 9001, false, true, 9001},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := portFor(tt.port, tt.demo, tt.portGiven); got != tt.want {
				t.Errorf("portFor(%d, %t, %t) = %d, want %d", tt.port, tt.demo, tt.portGiven, got, tt.want)
			}
		})
	}
}

// TestPortWasGiven: -port 8842 names the default and must still be honored, so
// what counts is whether the flag was set, not what it holds.
func TestPortWasGiven(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want bool
	}{
		{"not given", []string{"-demo"}, false},
		{"given", []string{"-port", "9001"}, true},
		{"given as the default", []string{"-port", strconv.Itoa(DefaultPort)}, true},
		{"given with an equals sign", []string{"-port=0"}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("checkbook", flag.ContinueOnError)
			fs.Int("port", DefaultPort, "")
			fs.Bool("demo", false, "")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("Parse(%v): %v", tt.args, err)
			}
			if got := portWasGiven(fs); got != tt.want {
				t.Errorf("portWasGiven(%v) = %t, want %t", tt.args, got, tt.want)
			}
		})
	}
}

// TestIsAddrInUse uses a real collision rather than a constructed error, so it
// keeps matching whatever the net package actually returns.
func TestIsAddrInUse(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer held.Close()

	_, err = net.Listen("tcp", held.Addr().String())
	if err == nil {
		t.Fatal("a second listener on the same address succeeded")
	}
	if !isAddrInUse(err) {
		t.Errorf("isAddrInUse(%v) = false, want true", err)
	}
}

func TestIsAddrInUseIgnoresOtherFailures(t *testing.T) {
	// A port far outside the valid range fails for a different reason, and must
	// not be reported as a checkbook that is already open.
	_, err := net.Listen("tcp", "127.0.0.1:999999")
	if err == nil {
		t.Fatal("listen on an invalid port succeeded")
	}
	if isAddrInUse(err) {
		t.Errorf("isAddrInUse(%v) = true, want false", err)
	}
}

func TestPortInUseMessage(t *testing.T) {
	var out strings.Builder
	if err := portInUse(&out, "127.0.0.1", DefaultPort); err == nil {
		t.Fatal("portInUse returned no error; the program did not start")
	}

	got := out.String()
	for _, want := range []string{
		strconv.Itoa(DefaultPort),                       // which port
		"http://127.0.0.1:" + strconv.Itoa(DefaultPort), // where the register is
		"already open",                                  // the likely cause
		"-port " + strconv.Itoa(DemoPort+1),             // what to do otherwise
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message does not mention %q:\n%s", want, got)
		}
	}
	// The suggested port skips the demo's, which is the one other port this
	// program is likely to be holding.
	if strings.Contains(got, "-port "+strconv.Itoa(DemoPort)) {
		t.Errorf("message sends the reader to the demo's port:\n%s", got)
	}
	// It must not claim to know what is listening: nothing was asked.
	if strings.Contains(got, "is open at") || strings.Contains(got, "is running at") {
		t.Errorf("message asserts what is listening without checking:\n%s", got)
	}
}

// TestPortInUseMessageIPv6 checks the address is built with brackets, so the
// reader can paste it into a browser.
func TestPortInUseMessageIPv6(t *testing.T) {
	var out strings.Builder
	_ = portInUse(&out, "::1", DefaultPort)

	if !strings.Contains(out.String(), "http://[::1]:"+strconv.Itoa(DefaultPort)+"/") {
		t.Errorf("IPv6 address is not bracketed:\n%s", out.String())
	}
}
