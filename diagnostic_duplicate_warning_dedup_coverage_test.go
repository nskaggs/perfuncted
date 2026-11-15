package perfuncted

import (
	"reflect"
	"testing"
)

// A failure bundle aggregates failures from independent capture components.
// A repeated failure category must not multiply its entry or make artifact
// ordering depend on the order in which capture workers reported errors.
func TestDiagnosticFailureCategoriesDeduplicateAndSortDeterministically(t *testing.T) {
	input := []string{"screen unavailable", "window denied", "screen unavailable", "accessibility timed out", "window denied", "", ""}
	copyInput := append([]string(nil), input...)
	want := []string{"", "accessibility timed out", "screen unavailable", "window denied"}
	for i := 0; i < 2; i++ {
		got := uniqueSortedStrings(input)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("duplicate diagnostic categories retained or unsorted: got=%q want=%q", got, want)
		}
	}
	if !reflect.DeepEqual(input, copyInput) {
		t.Fatalf("diagnostic normalization mutated caller failures: %q", input)
	}
}
