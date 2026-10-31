package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// These public wrappers must forward real session requests through the owner
// and return authoritative backend results, not just report availability.
func TestAccessibilityBundleSnapshotOutlineFocusedAndPointNormalAndFailure(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 19}
	target := accessibility.NodeID{BusName: id.BusName, ObjectPath: "/app/save", Generation: 19}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 19}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}},
		snapshot:                    accessibility.Snapshot{Root: accessibility.Node{ID: id, Role: "application", Name: "Editor", Children: []accessibility.NodeID{target}}, Nodes: []accessibility.Node{{ID: id, Role: "application", Name: "Editor", Children: []accessibility.NodeID{target}}, {ID: target, Parent: id, Role: "button", Name: "Save"}}, Generation: 19, Source: "synthetic-at-spi"},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	opts := accessibility.SnapshotOptions{MaxNodes: 8, MaxDepth: 3, MaxTextBytes: 256, Fresh: true}
	snap, err := session.Accessibility.Snapshot(context.Background(), id, opts)
	if err != nil || snap.Source != "synthetic-at-spi" || len(snap.Nodes) != 2 || fake.snapshotCalls != 1 {
		t.Fatalf("public snapshot returned %+v err=%v calls=%d", snap, err, fake.snapshotCalls)
	}
	outline, err := session.Accessibility.Outline(context.Background(), id, opts, accessibility.OutlineOptions{})
	if err != nil || fake.snapshotCalls != 2 || outline.Root.ID != id {
		t.Fatalf("public outline %+v err=%v calls=%d", outline, err, fake.snapshotCalls)
	}
	node, err := session.Accessibility.Focused(context.Background(), opts)
	if err != nil || node.Name != "focused" {
		t.Fatalf("public focused=%+v %v", node, err)
	}
	point, err := session.Accessibility.AtPoint(context.Background(), 13, 29)
	if err != nil || point.Name != "point" {
		t.Fatalf("public point=%+v %v", point, err)
	}
	failure := errors.New("synthetic accessibility provider refused observation")
	fake.snapshotErr = failure
	if _, err := session.Accessibility.Snapshot(context.Background(), id, opts); !errors.Is(err, failure) {
		t.Fatalf("snapshot provider error lost: %v", err)
	}
	if _, err := session.Accessibility.Outline(context.Background(), id, opts, accessibility.OutlineOptions{}); !errors.Is(err, failure) {
		t.Fatalf("outline snapshot provider error lost: %v", err)
	}
	if fake.snapshotCalls != 4 {
		t.Fatalf("snapshot calls %d, expected 4", fake.snapshotCalls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"snapshot", func(ctx context.Context) error { _, err := session.Accessibility.Snapshot(ctx, id, opts); return err }},
		{"outline", func(ctx context.Context) error {
			_, err := session.Accessibility.Outline(ctx, id, opts, accessibility.OutlineOptions{})
			return err
		}},
		{"focused", func(ctx context.Context) error { _, err := session.Accessibility.Focused(ctx, opts); return err }},
		{"atpoint", func(ctx context.Context) error { _, err := session.Accessibility.AtPoint(ctx, 13, 29); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation succeeded: %v", err)
			}
		})
	}
}
func TestAccessibilityBundleAutomationRejectsUnsupportedCapabilityAndClosedSession(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic.App", ObjectPath: "/button", Generation: 1}
	calls := []struct {
		name string
		call func(*AccessibilityBundle) error
	}{
		{"invoke index", func(b *AccessibilityBundle) error { return b.InvokeAction(context.Background(), id, 3) }},
		{"invoke by name", func(b *AccessibilityBundle) error {
			_, err := b.InvokeActionByName(context.Background(), id, "activate")
			return err
		}},
		{"default action", func(b *AccessibilityBundle) error {
			_, err := b.InvokeDefaultAction(context.Background(), id)
			return err
		}},
		{"scroll", func(b *AccessibilityBundle) error {
			return b.ScrollTo(context.Background(), id, accessibility.ScrollType(0))
		}},
		{"scroll point", func(b *AccessibilityBundle) error {
			return b.ScrollToPoint(context.Background(), id, accessibility.CoordType(0), 2, 3)
		}},
		{"position", func(b *AccessibilityBundle) error {
			return b.SetPosition(context.Background(), id, 3, 4, accessibility.CoordType(0))
		}},
		{"size", func(b *AccessibilityBundle) error { return b.SetSize(context.Background(), id, 20, 30) }},
		{"extents", func(b *AccessibilityBundle) error {
			return b.SetExtents(context.Background(), id, 1, 2, 3, 4, accessibility.CoordType(0))
		}},
		{"value", func(b *AccessibilityBundle) error { return b.SetValue(context.Background(), id, 1.5) }},
		{"insert", func(b *AccessibilityBundle) error { return b.InsertText(context.Background(), id, 0, "hello") }},
		{"delete", func(b *AccessibilityBundle) error { return b.DeleteText(context.Background(), id, 1, 4) }},
		{"copy", func(b *AccessibilityBundle) error { return b.CopyText(context.Background(), id, 1, 4) }},
		{"cut", func(b *AccessibilityBundle) error { return b.CutText(context.Background(), id, 1, 4) }},
		{"paste", func(b *AccessibilityBundle) error { return b.PasteText(context.Background(), id, 0) }},
		{"caret", func(b *AccessibilityBundle) error { return b.SetCaretOffset(context.Background(), id, 1) }},
		{"document selections", func(b *AccessibilityBundle) error { return b.SetTextSelections(context.Background(), id, nil) }},
		{"set selection", func(b *AccessibilityBundle) error { return b.SetTextSelection(context.Background(), id, 0, 0, 1) }},
		{"add selection", func(b *AccessibilityBundle) error { return b.AddTextSelection(context.Background(), id, 0, 1) }},
		{"remove selection", func(b *AccessibilityBundle) error { return b.RemoveTextSelection(context.Background(), id, 0) }},
		{"select child", func(b *AccessibilityBundle) error { return b.SelectChild(context.Background(), id, 1) }},
		{"deselect child", func(b *AccessibilityBundle) error { return b.DeselectChild(context.Background(), id, 1) }},
		{"select all", func(b *AccessibilityBundle) error { return b.SelectAll(context.Background(), id) }},
		{"clear selection", func(b *AccessibilityBundle) error { return b.ClearSelection(context.Background(), id) }},
		{"deselect selected", func(b *AccessibilityBundle) error { return b.DeselectSelectedChild(context.Background(), id) }},
		{"select row", func(b *AccessibilityBundle) error { return b.SelectRow(context.Background(), id, 1) }},
		{"deselect row", func(b *AccessibilityBundle) error { return b.DeselectRow(context.Background(), id, 1) }},
		{"select column", func(b *AccessibilityBundle) error { return b.SelectColumn(context.Background(), id, 1) }},
		{"deselect column", func(b *AccessibilityBundle) error { return b.DeselectColumn(context.Background(), id, 1) }},
		{"focus", func(b *AccessibilityBundle) error { return b.FocusNode(context.Background(), id) }},
		{"replace text", func(b *AccessibilityBundle) error { return b.ReplaceEditableText(context.Background(), id, "text") }},
	}
	// A read-only AT-SPI backend cannot silently fall back to physical input.
	readonly := NewSessionForTesting(nil, nil, nil, nil, nil, &bundleAccessibilityFake{gen: 1})
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(readonly.Accessibility)
			if err == nil || (!errors.Is(err, accessibility.ErrUnsupported) && !strings.Contains(err.Error(), "unsupported")) {
				t.Fatalf("read-only backend executed %s: %v", tc.name, err)
			}
		})
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range calls {
		t.Run("closed-"+tc.name, func(t *testing.T) {
			if err := tc.call(readonly.Accessibility); err == nil {
				t.Fatalf("closed backend accepted %s", tc.name)
			}
		})
	}
}
