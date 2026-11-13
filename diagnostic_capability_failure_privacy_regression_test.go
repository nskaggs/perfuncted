package perfuncted

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Capability failures can include provider-returned titles, paths or tokens.
// The opt-in diagnostic manifest must retain the fact of failure without
// copying arbitrary provider error text into a long-lived artifact.
func TestDiagnosticCapabilityFailureDoesNotPublishProviderErrorPayload(t *testing.T) {
	const secret = "synthetic-private-document-access-token-7ec91"
	statuses := []CapabilityStatus{{
		Capability: CapabilityAccessibility,
		Requested:  true,
		Required:   true,
		Available:  false,
		Failure:    errors.New("provider rejected private payload: " + secret),
	}}
	entries := diagnosticCapabilities(statuses)
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "provider rejected private payload") {
		t.Fatalf("capability manifest leaked provider error: %s", data)
	}
	if len(entries) != 1 || entries[0].Failure == "" || entries[0].Available || !entries[0].Required {
		t.Fatalf("capability failure classification lost: %+v", entries)
	}
}
