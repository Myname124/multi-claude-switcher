package main

import "testing"

func TestHasSkipInstanceFlag(t *testing.T) {
	if !hasSkipInstanceFlag([]string{"/path/mcs-tray", relaunchSkipInstanceCheckFlag}) {
		t.Error("expected true when the flag is present")
	}
	if hasSkipInstanceFlag([]string{"/path/mcs-tray"}) {
		t.Error("expected false when the flag is absent")
	}
}
