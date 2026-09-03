package window

import (
	"strings"
	"testing"
)

func TestSignedNumericIDRejectsOverflow(t *testing.T) {
	if _, err := signedNumericID("9223372036854775808"); err == nil {
		t.Fatal("signedNumericID accepted a value above MaxInt64")
	}
}

func TestX11WindowIDRejectsOverflow(t *testing.T) {
	_, err := x11WindowID("4294967296")
	if err == nil || !strings.Contains(err.Error(), "X11 window id range") {
		t.Fatalf("x11WindowID error = %v, want X11 range error", err)
	}
}

func TestSwayIterateWindowsSkipsNegativeIDs(t *testing.T) {
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(`{"id":1,"type":"root","nodes":[{"id":-1,"type":"con","name":"bad"},{"id":2,"type":"con","name":"good"}]}`))}
	var got []Info
	for info, err := range manager.IterateWindows(nil) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, info)
	}
	if len(got) != 1 || got[0].Title != "good" {
		t.Fatalf("IterateWindows = %#v, want only good window", got)
	}
}
