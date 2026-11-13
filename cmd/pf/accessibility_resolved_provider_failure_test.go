package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type resolvedAccessibilityFailureBackend struct {
	cliAutomationFake
	failure   error
	resolves  int
	snapshots int
	finds     int
}

func (f *resolvedAccessibilityFailureBackend) FindApplication(_ context.Context, _ accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.resolves++
	return accessibility.Application{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}, Name: "Editor", Role: "application"}, PID: 42}, nil
}
func (f *resolvedAccessibilityFailureBackend) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshots++
	return accessibility.Snapshot{}, f.failure
}
func (f *resolvedAccessibilityFailureBackend) Find(_ context.Context, _ accessibility.NodeID, _ accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.finds++
	return nil, f.failure
}

// Resolving the requested app is not proof that its contents are readable.
// A failed follow-on read must not serialize a partial tree, match list, or
// acknowledge a semantic operation that has never observed a target.
func TestAccessibilityResolvedScopeProviderFailuresDoNotPublishResults(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		args                     []string
		wantSnapshots, wantFinds int
	}{
		{"tree snapshot", []string{"a11y", "tree", "--app", "Editor"}, 1, 0},
		{"tree JSON snapshot", []string{"a11y", "tree", "--app", "Editor", "--json"}, 1, 0},
		{"find semantic query", []string{"a11y", "find", "--app", "Editor", "--role", "button"}, 1, 0},
		{"find JSON query", []string{"a11y", "find", "--app", "Editor", "--role", "button", "--json"}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &resolvedAccessibilityFailureBackend{failure: errors.New("synthetic provider snapshot unavailable")}
			opens := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, f), nil
				}
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, f.failure.Error()) || opens != 1 || f.resolves != 1 || f.snapshots != tc.wantSnapshots || f.finds != tc.wantFinds {
				t.Fatalf("unavailable resolved tree/query args=%v exit=%d out=%q err=%q opens=%d resolves=%d snapshots=%d finds=%d", tc.args, exit, stdout, stderr, opens, f.resolves, f.snapshots, f.finds)
			}
		})
	}
}
