// Copyright (c) 2026 Michael D Henderson.

package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

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
		"-port " + strconv.Itoa(DefaultPort+1),          // what to do otherwise
	} {
		if !strings.Contains(got, want) {
			t.Errorf("message does not mention %q:\n%s", want, got)
		}
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
