package env

import (
	"testing"
)

func TestMergeOverrides(t *testing.T) {
	t.Parallel()
	base := []string{"A=1", "B=2", "C=3"}
	over := []string{"B=20", "D=4"}
	got := Merge(base, over...)
	// Expect A=1, C=3 preserved, B overridden to 20, D appended
	m := make(map[string]string)
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if m["A"] != "1" {
		t.Fatalf("A = %q, want 1", m["A"])
	}
	if m["B"] != "20" {
		t.Fatalf("B = %q, want 20", m["B"])
	}
	if m["C"] != "3" {
		t.Fatalf("C = %q, want 3", m["C"])
	}
	if m["D"] != "4" {
		t.Fatalf("D = %q, want 4", m["D"])
	}
}

func TestMergeClears(t *testing.T) {
	t.Parallel()
	base := []string{"X=old", "Y=keep"}
	over := []string{"X="}
	got := Merge(base, over...)
	m := make(map[string]string)
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	if _, ok := m["X"]; !ok {
		t.Fatalf("X missing after merge")
	}
	if m["X"] != "" {
		t.Fatalf("X = %q, want empty", m["X"])
	}
	if m["Y"] != "keep" {
		t.Fatalf("Y = %q, want keep", m["Y"])
	}
}

func TestMergeEmptyBase(t *testing.T) {
	t.Parallel()
	got := Merge(nil, "A=1", "B=2")
	m := envMap(got)
	if m["A"] != "1" || m["B"] != "2" {
		t.Fatalf("Merge(nil) = %v", got)
	}
}

func TestMergeLastOverrideWins(t *testing.T) {
	t.Parallel()
	got := Merge([]string{"A=base"}, "A=first", "A=second")
	if m := envMap(got); m["A"] != "second" {
		t.Fatalf("A = %q, want second", m["A"])
	}
}

func TestMergeOverlayReplacesKeyOnlyBaseEntry(t *testing.T) {
	t.Parallel()

	got := Merge([]string{"FOO", "BAR=1"}, "FOO=2")
	want := []string{"BAR=1", "FOO=2"}
	if len(got) != len(want) {
		t.Fatalf("Merge returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Merge[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func TestMergePreservesKeyOnlyBaseEntryWithoutOverlay(t *testing.T) {
	t.Parallel()

	got := Merge([]string{"FOO", "BAR=1"})
	want := []string{"FOO", "BAR=1"}
	if len(got) != len(want) {
		t.Fatalf("Merge returned %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Merge[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

func envMap(kvs []string) map[string]string {
	m := make(map[string]string)
	for _, kv := range kvs {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
