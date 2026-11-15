package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A direct helper call must reject revoked work before parsing a request or
// consulting an uninitialized X11 connection.
func TestXTestInternalTypePreflightRejectsCanceledAndMalformedRequests(t *testing.T) {
	var backend XTestBackend
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.typeContext(canceled, "{unclosed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled internal type parsed request: %v", err)
	}
	if err := backend.typeContext(context.Background(), "{unclosed"); err == nil {
		t.Fatal("malformed internal type request was accepted")
	}
}

// Empty actions must remain no-ops, and malformed direct actions must not
// attempt event injection on an unavailable X11 connection.
func TestXTestInternalTypeActionNoopAndUnknownKeyBeforeTransport(t *testing.T) {
	var backend XTestBackend
	if err := backend.typeAction(context.Background(), keySend{}); err != nil {
		t.Fatalf("empty action was rejected: %v", err)
	}
	if err := backend.typeAction(context.Background(), keySend{key: "unknown-key"}); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key reached XTEST transport: %v", err)
	}
}
