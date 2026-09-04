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
func (cliAccessibilityFake) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	stream := make(chan accessibility.Event)
	close(stream)
	return stream, nil
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

func openCLIWithAccessibility(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAccessibilityFake{}), nil
	}
}

func TestAccessibilityCLICommandsJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "snapshot", args: []string{"accessibility", "snapshot", "--max-depth", "2"}, want: "root"},
		{name: "tree alias", args: []string{"accessibility", "tree"}, want: "root"},
		{name: "find", args: []string{"accessibility", "find", "--role", "button", "--name", "Save"}, want: "Save"},
		{name: "focused", args: []string{"accessibility", "focused"}, want: "focused"},
		{name: "at point", args: []string{"accessibility", "at-point", "--x", "10", "--y", "20"}, want: "point"},
		{name: "events", args: []string{"accessibility", "events"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := captureRunIO(t, tt.args, openCLIWithAccessibility)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Fatalf("stdout=%q, want substring %q", stdout, tt.want)
			}
		})
	}
}

func TestAccessibilityCLIRejectsPartialRoot(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "snapshot", "--root-bus", "org.test"}, openCLIWithAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "root requires both") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
