package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type rejectingAccessibilityWriter struct{ err error }

func (w rejectingAccessibilityWriter) Write([]byte) (int, error) { return 0, w.err }

// A failed output sink must interrupt every format, including nested outlines
// and action receipts; do not acknowledge success after partial serialization.
func TestAccessibilityOutputNeverAcknowledgesFailedSink(t *testing.T) {
	sentinel := errors.New("synthetic output transport disconnected")
	node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/node", Generation: 7}, Role: "button", Name: "Save"}
	outline := accessibility.Outline{Root: accessibility.OutlineNode{ID: node.ID, Role: "application", Children: []accessibility.OutlineNode{{ID: node.ID, Role: "button", Name: "Save"}}}}
	for _, tc := range []struct {
		name  string
		value any
		json  bool
	}{
		{"applications", []accessibility.Application{{Node: node, PID: 42}}, false},
		{"node", node, false},
		{"nodes", []accessibility.Node{node, node}, false},
		{"snapshot", accessibility.Snapshot{Root: node, Nodes: []accessibility.Node{node}, Generation: 7}, false},
		{"outline", outline, false},
		{"partial outline", accessibility.Outline{Generation: 7, Truncated: true, Root: accessibility.OutlineNode{ID: node.ID, Role: "app"}}, false},
		{"action", accessibility.Action{Index: 4, Name: "press"}, false},
		{"receipt", perfuncted.AccessibilityActionReceipt{Node: node, Action: accessibility.Action{Index: 4, Name: "press"}}, false},
		{"fallback", struct{ Value int }{Value: 19}, false},
		{"JSON", node, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := accessibilityOutput(rejectingAccessibilityWriter{err: sentinel}, tc.json, tc.value)
			if !errors.Is(err, sentinel) {
				t.Fatalf("failed sink acknowledged: %v", err)
			}
		})
	}
}

func TestAccessibilitySessionOpenerPropagatesFailedAuthorization(t *testing.T) {
	denied := errors.New("synthetic inaccessible AT-SPI bus")
	opens := 0
	for _, tc := range []struct {
		name string
		open sessionOpener
		want string
	}{
		{"provider denied", func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }, "denied"},
		{"nil session", func(context.Context) (*perfuncted.Session, error) { opens++; return nil, nil }, "capability unavailable"},
		{"session missing accessibility", func(context.Context) (*perfuncted.Session, error) {
			opens++
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil), nil
		}, "capability unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := openAccessibilitySession(context.Background(), tc.open)
			if session != nil || err == nil {
				t.Fatalf("denied session accepted: session=%v err=%v", session, err)
			}
			if tc.name == "provider denied" && !errors.Is(err, denied) {
				t.Fatalf("provider denial lost: %v", err)
			}
		})
	}
	if opens != 3 {
		t.Fatalf("expected exactly one open per scenario: %d", opens)
	}
}
