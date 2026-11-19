package perfuncted

import (
	"context"
	"strings"
	"testing"
)

// An unknown capability must be rejected before a backend opener, session
// registry, or desktop process can be touched.
func TestSessionOpenCapabilityRejectsUnknownIdentifierWithoutBackendMutation(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	unknown := Capability("future-unsupported-backend")
	for _, open := range []struct {
		name string
		call func() (any, error)
	}{
		{name: "contextual", call: func() (any, error) {
			return session.openCapabilityContext(context.Background(), unknown)
		}},
		{name: "legacy", call: func() (any, error) {
			return session.openCapability(unknown)
		}},
	} {
		t.Run(open.name, func(t *testing.T) {
			backend, err := open.call()
			if backend != nil || err == nil || !strings.Contains(err.Error(), "unknown capability") {
				t.Fatalf("unknown capability exposed backend=%v error=%v", backend, err)
			}
		})
	}
	if session.Has(unknown) {
		t.Fatal("unknown backend became available after rejected open")
	}
}
