package main

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestCLIStableIDLeadingZeroDecimalDoesNotSelectWrongWindow(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "8", ID: 8, Title: "wrong"}, {NativeID: "10", ID: 10, Title: "correct"}}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.CachedInfo().ID; id != 10 {
		t.Fatalf("decimal ID 010 selected window %d, want 10", id)
	}
}

func TestCLINumericHashLeadingZeroDecimal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint32
	}{{"010", 10}, {"08", 8}, {"0x10", 16}, {"abcdef", 0xabcdef}} {
		got, err := parseHash(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("parseHash(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
		}
	}
}

func TestCLIStableIDPrefersExactOpaqueHandleOverNumericAlias(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{
		{NativeID: "10", ID: 10, Title: "numeric alias"},
		{NativeID: "010", Title: "exact opaque handle"},
	}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.ID().String(); id != "010" {
		t.Fatalf("selected native ID %q, want exact 010", id)
	}
}
