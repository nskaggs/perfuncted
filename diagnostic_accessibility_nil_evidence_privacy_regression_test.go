package perfuncted

import (
	"encoding/json"
	"testing"
)

// Missing accessibility evidence must never manufacture diagnostic records
// or emit a JSON null in place of the expected empty structured artifact.
func TestAccessibilityFailureDiagnosticMissingEvidenceIsEmptyObject(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence *AccessibilityFailureEvidence
	}{
		{name: "nil evidence"},
		{name: "empty evidence", evidence: &AccessibilityFailureEvidence{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifact := summarizeAccessibilityFailure(tc.evidence)
			if artifact.Locator != nil || artifact.Snapshot != nil || artifact.Action != nil || artifact.Postcondition != nil || len(artifact.Events) != 0 || artifact.EventsOmitted != 0 {
				t.Fatalf("missing evidence produced diagnostic contents: %+v", artifact)
			}
			payload, err := json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != "{}" {
				t.Fatalf("missing evidence emitted unexpected JSON: %s", payload)
			}
		})
	}
}
