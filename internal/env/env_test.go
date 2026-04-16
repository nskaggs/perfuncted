package env

import (
	"testing"
)

func TestMergeOverrides(t *testing.T) {
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
