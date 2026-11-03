package main

import (
	"strings"
	"testing"
)

func TestParseWindowMatchArgsPreservesMultiwordCLISelection(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"title=Project Notes", "app_id=org.example.Editor", "visible", "active=false"})
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleExact != "Project Notes" || got.AppID != "org.example.Editor" || !got.VisibleOnly || got.Active == nil || *got.Active {
		t.Fatalf("window selection lost CLI fields: %+v", got)
	}
	empty, err := parseWindowMatchArgs(nil)
	if err != nil || empty.TitleExact != "" || empty.Active != nil || empty.VisibleOnly {
		t.Fatalf("empty match: %+v %v", empty, err)
	}
}

func TestParseWindowMatchArgsRejectsInvalidPIDWithoutMatchingEverything(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"pid=not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "parse pid") {
		t.Fatalf("malformed PID accepted: %+v %v", got, err)
	}
	if got.PID != nil || got.TitleContains != "" || got.TitleExact != "" {
		t.Fatalf("error returned partial match: %+v", got)
	}
}

func TestParseWindowMatchArgsPreservesQuotedPositionalTitle(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"Quarterly Planning", "active"})
	if err != nil {
		t.Fatalf("quoted positional title should be one token: %v", err)
	}
	if got.TitleContains != "Quarterly Planning" || got.Active == nil || !*got.Active {
		t.Fatalf("quoted title match altered: %+v", got)
	}
}
