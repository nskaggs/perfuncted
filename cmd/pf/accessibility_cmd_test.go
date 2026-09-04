package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type cliAccessibilityFake struct{}

func (cliAccessibilityFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events"}
}
func (cliAccessibilityFake) Applications(context.Context) ([]accessibility.Application, error) {
	return []accessibility.Application{{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app"}, Name: "Test App", Role: "application"}, PID: 123}}, nil
}
func (cliAccessibilityFake) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	return accessibility.Snapshot{Root: accessibility.Node{Name: "root"}, Nodes: []accessibility.Node{{Name: "root"}}, Generation: 1, Source: "fake"}, nil
}
func (cliAccessibilityFake) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return []accessibility.Node{{Name: "Save", Role: "button"}}, nil
}
func (cliAccessibilityFake) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Name: "focused"}, nil
}
func (cliAccessibilityFake) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{Name: "point"}, nil
}
func (cliAccessibilityFake) Close() error { return nil }

func TestAccessibilityCLIApplicationsJSON(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "applications"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAccessibilityFake{}), nil
		}
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var apps []accessibility.Application
	if err := json.Unmarshal([]byte(stdout), &apps); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if len(apps) != 1 || apps[0].PID != 123 || !strings.Contains(apps[0].Name, "Test") {
		t.Fatalf("apps=%+v", apps)
	}
}
