package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type cancelOnIdleEventSubscription struct {
	cliAccessibilityFake
	subscribed chan<- struct{}
}

// The provider accepts a live subscription but may emit no invalidation
// events. Cancellation must still end the public command immediately without
// waiting for a message or fabricating a successful stream completion.
func (p cancelOnIdleEventSubscription) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	events := make(chan accessibility.Event)
	close(p.subscribed)
	return events, nil
}

func TestAccessibilityEventsCancelWhileSubscriptionIsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed := make(chan struct{})
	provider := cancelOnIdleEventSubscription{subscribed: subscribed}
	command := accessibilityCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, provider), nil
	}, nil)
	command.SilenceErrors = true
	command.SilenceUsage = true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"events"})
	result := make(chan error, 1)
	go func() { result <- command.ExecuteContext(ctx) }()
	<-subscribed
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled idle accessibility events returned %v, want cancellation", err)
	}
	if output.Len() != 0 {
		t.Fatalf("idle canceled stream emitted %q", output.String())
	}
}
