package perfuncted

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// Optional nil configuration entries must not prevent a host session from
// opening, and explicitly enabled action traces must use the caller's writer.
func TestOpenIgnoresNilOptionAndRoutesOwnedTrace(t *testing.T) {
	var writer bytes.Buffer
	session, err := Open(context.Background(), nil, WithTrace(true), WithTraceOut(&writer))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.tracer == nil {
		t.Fatal("requested trace writer did not initialize")
	}
	session.Input.traceAction("input", "key-up %s", "Return")
	if got := writer.String(); !strings.Contains(got, "input key-up Return") {
		t.Fatalf("session did not route trace to configured writer: %q", got)
	}
	if session.Has(CapabilityInput) {
		t.Fatal("an unrequested input backend became available")
	}
}
