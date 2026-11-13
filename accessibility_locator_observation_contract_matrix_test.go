package perfuncted

import (
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A locator must never accept a target after an invalidation, a closed
// subscription, or a missing subscription. An empty live stream is the only
// observation state that permits the caller to proceed with a fresh read.
func TestLocatorObservationContractRejectsMissingClosedAndInvalidatedStreams(t *testing.T) {
	live := make(chan accessibility.Event, 1)
	closed := make(chan accessibility.Event)
	close(closed)
	invalidated := make(chan accessibility.Event, 1)
	invalidated <- accessibility.Event{Kind: "object:children-changed"}
	for _, tc := range []struct {
		name        string
		stream      <-chan accessibility.Event
		wantChanged bool
	}{
		{name: "nil subscription", wantChanged: true},
		{name: "closed subscription", stream: closed, wantChanged: true},
		{name: "queued invalidation", stream: invalidated, wantChanged: true},
		{name: "live empty subscription", stream: live},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := locatorObservationReady(tc.stream)
			if got := errors.Is(err, accessibility.ErrObservationChanged); got != tc.wantChanged {
				t.Fatalf("observation error = %v; changed = %t, want %t", err, got, tc.wantChanged)
			}
			if !tc.wantChanged && err != nil {
				t.Fatalf("live observation unexpectedly rejected: %v", err)
			}
		})
	}
}

func TestLocatorRetriesOnlyObservationAndStaleIdentityFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"stale generation", errors.Join(errors.New("wrapped"), accessibility.ErrStaleGeneration), true},
		{"stale node", errors.Join(errors.New("wrapped"), accessibility.ErrStaleNode), true},
		{"observation changed", errors.Join(errors.New("wrapped"), accessibility.ErrObservationChanged), true},
		{"unsupported", accessibility.ErrUnsupported, false},
		{"invalid argument", ErrInvalidArgument, false},
		{"generic provider failure", errors.New("provider unavailable"), false},
		{"success", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := locatorResolutionMayRetry(tc.err); got != tc.retry {
				t.Fatalf("retry(%v) = %t, want %t", tc.err, got, tc.retry)
			}
		})
	}
}
