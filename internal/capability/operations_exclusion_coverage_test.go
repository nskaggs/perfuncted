package capability

import (
	"slices"
	"testing"
)

func TestOperationsExclusionsAreOrderIndependentAndDoNotMutateRegistry(t *testing.T) {
	all := Operations("input")
	without := Operations("input", "pointer", "keyboard", "pointer", "not-an-operation")
	if slices.Contains(without, "pointer") || slices.Contains(without, "keyboard") {
		t.Fatalf("excluded operations remain: %v", without)
	}
	if len(without) != len(all)-2 {
		t.Fatalf("excluded duplicate modified length: all=%v filtered=%v", all, without)
	}
	if !slices.Contains(Operations("input"), "pointer") {
		t.Fatal("exclusion modified registry")
	}
	if result := Operations("nonexistent", "keyboard"); len(result) != 0 {
		t.Fatalf("unknown surface returned operations: %v", result)
	}
}
