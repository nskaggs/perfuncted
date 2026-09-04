package accessibility

import (
	"testing"
	"time"
)

func TestSnapshotOptionsNormalizeBounds(t *testing.T) {
	got := (SnapshotOptions{MaxDepth: 1000, MaxNodes: 1 << 30, MaxTextBytes: 1 << 30}).normalized()
	if got.MaxDepth != absMaxDepth || got.MaxNodes != absMaxNodes || got.MaxTextBytes != absMaxText {
		t.Fatalf("normalized limits = %+v, want hard limits", got)
	}
	defaults := (SnapshotOptions{}).normalized()
	if defaults.MaxDepth != defaultMaxDepth || defaults.MaxNodes != defaultMaxNodes || defaults.MaxTextBytes != defaultMaxText {
		t.Fatalf("defaults = %+v", defaults)
	}
}

func TestNodeIDValidation(t *testing.T) {
	if (NodeID{}).valid() {
		t.Fatal("zero NodeID is valid")
	}
	if (NodeID{BusName: "org.example", ObjectPath: string(nullObjectPath)}).valid() {
		t.Fatal("null AT-SPI object is valid")
	}
	if !(NodeID{BusName: "org.example", ObjectPath: "/org/example/node"}).valid() {
		t.Fatal("ordinary AT-SPI object is invalid")
	}
}

func TestEventOptionsNormalizeAndSignalConversion(t *testing.T) {
	if got := (EventOptions{}).normalized().Buffer; got != defaultEventBuffer {
		t.Fatalf("default event buffer = %d", got)
	}
	if got := (EventOptions{Buffer: 99999}).normalized().Buffer; got != 4096 {
		t.Fatalf("capped event buffer = %d", got)
	}
	e := signalEvent(nil)
	if e.Timestamp.IsZero() {
		t.Fatal("nil signal did not get timestamp")
	}
	if !hasSensitiveState([]string{"visible", "protected"}) || hasSensitiveState([]string{"visible"}) {
		t.Fatal("sensitive state detection incorrect")
	}
}

func TestCloneSnapshotDeepCopiesAndPreservesMetadata(t *testing.T) {
	at := time.Now()
	in := Snapshot{Root: Node{ID: NodeID{BusName: "b", ObjectPath: "/r"}}, Nodes: []Node{{ID: NodeID{BusName: "b", ObjectPath: "/r"}, Attributes: map[string]string{"value": "x"}, Children: []NodeID{{BusName: "b", ObjectPath: "/c"}}}}, Generation: 4, CapturedAt: at, Source: "at-spi"}
	out := cloneSnapshot(in)
	out.Nodes[0].Attributes["value"] = "changed"
	out.Nodes[0].Children[0].ObjectPath = "/changed"
	if in.Nodes[0].Attributes["value"] != "x" || in.Nodes[0].Children[0].ObjectPath != "/c" {
		t.Fatal("clone shares mutable node state")
	}
	if out.Generation != 4 || out.Source != "at-spi" || !out.CapturedAt.Equal(at) {
		t.Fatalf("metadata not preserved: %+v", out)
	}
}

func TestSnapshotKeySeparatesSecurityAndLimits(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/r"}
	if snapshotKey(root, SnapshotOptions{MaxDepth: 1}) == snapshotKey(root, SnapshotOptions{MaxDepth: 2}) {
		t.Fatal("depth not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{AllowSensitive: true}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("redaction policy not part of snapshot key")
	}
}

func TestEventOptionsRejectNilContext(t *testing.T) {
	b := &dbusBackend{}
	if _, err := b.Events(nil, EventOptions{}); err == nil || err.Error() != "accessibility: nil context" {
		t.Fatalf("Events(nil) error = %v", err)
	}
}
