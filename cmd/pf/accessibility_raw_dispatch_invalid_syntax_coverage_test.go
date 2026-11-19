package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Raw operations are a defense-in-depth boundary: even direct dispatch must
// reject malformed local parameters before touching an accessibility backend.
func TestRawAccessibilityDispatchRejectsMalformedInputsBeforeBackend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request rawInvocation
		message string
	}{
		{"scroll alignment", rawInvocation{op: "scroll", alignment: "bogus-alignment"}, "unknown alignment"},
		{"scroll point space", rawInvocation{op: "scroll-to-point", coord: "bogus-space"}, "unknown coordinate space"},
		{"position space", rawInvocation{op: "set-position", coord: "bogus-space"}, "unknown coordinate space"},
		{"extents space", rawInvocation{op: "set-extents", coord: "bogus-space"}, "unknown coordinate space"},
		{"malformed selections", rawInvocation{op: "set-document-text-selections", selectionsJSON: "{not json"}, "invalid --selections JSON"},
		{"unknown operation", rawInvocation{op: "unrecognized-op"}, "unknown raw op"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := invokeRawOp(context.Background(), nil, accessibility.NodeID{}, tc.request)
			if value != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("malformed request returned value=%v err=%v (want %q)", value, err, tc.message)
			}
		})
	}
}
