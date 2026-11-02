package env

import (
	"slices"
	"testing"
)

func TestMergeDuplicateKeyOnlyEntriesAndMissingEqualsOverlays(t *testing.T) {
	base := []string{"KEY", "KEEP=1", "SKIP", "OTHER=3", "KEEP=stale"}
	merged := Merge(base, "SKIP", "KEY=first", "KEY=last", "SKIP=overwritten")
	want := []string{"KEEP=1", "OTHER=3", "KEEP=stale", "SKIP=overwritten", "KEY=last"}
	if !slices.Equal(merged, want) {
		t.Fatalf("Merge invalid key-only overlay got=%v want=%v", merged, want)
	}
	// A key-only overlay is a valid explicit empty value and retains its
	// position when a later overlay of the same key supplies a value.
	if got := Merge([]string{"A=original", "B=keep"}, "A", "A=again"); !slices.Equal(got, []string{"B=keep", "A=again"}) {
		t.Fatalf("repeated key-only overlay got=%v", got)
	}
	if got := Merge([]string{"KEY"}, "NOVALUE"); !slices.Equal(got, []string{"KEY", "NOVALUE="}) {
		t.Fatalf("key-only overlay got=%v", got)
	}
}
