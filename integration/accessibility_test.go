//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

// TestAccessibilityManagedSession exercises the live AT-SPI surface inside
// the managed display session. Minimal CI images may not ship at-spi-bus-
// launcher; the capability is intentionally optional, so those environments
// still record the attempted resolution and skip the live protocol checks.
func TestAccessibilityManagedSession(t *testing.T) {
	s := mustSuite(t)
	status := s.pf.Capability("accessibility")
	if !status.Requested || status.Required {
		t.Fatalf("managed suite must request accessibility optionally: %+v", status)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if !status.Available {
		if _, err := s.pf.Accessibility.Applications(ctx); !errors.Is(err, perfuncted.ErrUnavailable) {
			t.Fatalf("unavailable AT-SPI applications should retain typed error: %v", err)
		}
		t.Logf("AT-SPI unavailable in managed %s session (optional): %v", s.mode, status.Failure)
		return
	}

	for _, operation := range []string{
		"applications",
		"snapshot",
		"find",
		"find-application",
		"focused",
		"at-point",
		"events",
	} {
		if !status.Supports(operation) {
			t.Fatalf("AT-SPI backend omitted advertised operation %q: %+v", operation, status)
		}
	}

	apps, err := s.pf.Accessibility.Applications(ctx)
	if err != nil {
		t.Fatalf("AT-SPI applications: %v", err)
	}

	options := accessibility.SnapshotOptions{MaxDepth: 8, MaxNodes: 256, MaxTextBytes: 512}
	snapshot, err := s.pf.Accessibility.Snapshot(ctx, accessibility.NodeID{}, options)
	if err != nil {
		t.Fatalf("AT-SPI snapshot: %v", err)
	}
	if snapshot.Source != "at-spi" || snapshot.Generation == 0 || snapshot.CapturedAt.IsZero() {
		t.Fatalf("AT-SPI snapshot missing provenance: %+v", snapshot)
	}
	if _, err := s.pf.Accessibility.Find(ctx, snapshot.Root.ID, accessibility.Query{Role: "application"}, options); err != nil {
		t.Fatalf("AT-SPI find: %v", err)
	}

	if _, err := s.pf.Accessibility.Focused(ctx, options); err != nil && !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("AT-SPI focused: %v", err)
	}
	if _, err := s.pf.Accessibility.AtPoint(ctx, 0, 0); err != nil && !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("AT-SPI at-point: %v", err)
	}
	if len(apps) > 0 && apps[0].ID.BusName != "" {
		if _, err := s.pf.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{Bus: apps[0].ID.BusName}); err != nil {
			t.Fatalf("AT-SPI find-application: %v", err)
		}
	}

	eventCtx, stopEvents := context.WithCancel(ctx)
	events, err := s.pf.Accessibility.Events(eventCtx, accessibility.EventOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("AT-SPI events: %v", err)
	}
	stopEvents()
	select {
	case _, ok := <-events:
		if ok {
			for range events {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AT-SPI event stream did not close after cancellation")
	}
}
