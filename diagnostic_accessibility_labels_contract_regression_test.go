package perfuncted

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Diagnostic metadata is bounded before it is exposed to callers. Empty
// collections stay absent rather than becoming an allocated empty list.
func TestDiagnosticLabelsBoundedUnicodeAndEmptyContract(t *testing.T) {
	if labels := diagnosticLabels(nil); labels != nil {
		t.Fatalf("nil diagnostic labels must remain nil: %#v", labels)
	}
	if labels := diagnosticLabels([]string{}); labels != nil {
		t.Fatalf("empty diagnostic labels must remain nil: %#v", labels)
	}

	long := strings.Repeat("界", maxDiagnosticLabelBytes)
	values := make([]string, maxDiagnosticListItems+5)
	for i := range values {
		values[i] = long
	}
	labels := diagnosticLabels(values)
	if len(labels) != maxDiagnosticListItems {
		t.Fatalf("diagnostic list size = %d, want %d", len(labels), maxDiagnosticListItems)
	}
	for i, label := range labels {
		if len(label) > maxDiagnosticLabelBytes || !utf8.ValidString(label) || label != strings.Repeat("界", maxDiagnosticLabelBytes/len("界")) {
			t.Fatalf("label %d improperly truncated: %q (%d bytes)", i, label, len(label))
		}
	}
}
