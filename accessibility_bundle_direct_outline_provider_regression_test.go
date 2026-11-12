package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type directOutlineAccessibilityProbe struct {
	*snapshotAccessibilityAutomationFake
	calls       int
	gotRoot     accessibility.NodeID
	gotSnapshot accessibility.SnapshotOptions
	gotOutline  accessibility.OutlineOptions
	response    accessibility.Outline
	failure     error
}

func (p *directOutlineAccessibilityProbe) Outline(_ context.Context, root accessibility.NodeID, snap accessibility.SnapshotOptions, opts accessibility.OutlineOptions) (accessibility.Outline, error) {
	p.calls++
	p.gotRoot = root
	p.gotSnapshot = snap
	p.gotOutline = opts
	return p.response, p.failure
}

// An explicitly exposed provider Outline must be used instead of a second
// snapshot traversal. Provider errors must be surfaced without silently
// replacing a failed authoritative outline with potentially stale data.
func TestAccessibilityBundleDirectOutlineProviderPreservesScopeAndFailures(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/editor", Generation: 11}
	snapshotOpts := accessibility.SnapshotOptions{MaxDepth: 4, MaxNodes: 16, Fresh: true}
	outlineOpts := accessibility.OutlineOptions{MaxDepth: 2, MaxNodes: 8}
	underlying := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 11}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}},
	}
	p := &directOutlineAccessibilityProbe{snapshotAccessibilityAutomationFake: underlying, response: accessibility.Outline{Root: accessibility.OutlineNode{ID: root}, Generation: 11, Truncated: true, ProviderErrors: 2, Warnings: []string{"synthetic provider incomplete"}}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, p)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = p
	defer func() { session.Accessibility.backend = owner }()
	actual, err := session.Accessibility.Outline(context.Background(), root, snapshotOpts, outlineOpts)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Generation != 11 || !actual.Truncated || actual.ProviderErrors != 2 || len(actual.Warnings) != 1 || actual.Root.ID != root {
		t.Fatalf("provider outline lost truncation or provenance: %+v", actual)
	}
	if p.calls != 1 || p.gotRoot != root || !reflect.DeepEqual(p.gotSnapshot, snapshotOpts) || p.gotOutline != outlineOpts || underlying.snapshotCalls != 0 {
		t.Fatalf("provider delegation calls=%d root=%+v snapshotOpts=%+v outlineOpts=%+v fallbackCalls=%d", p.calls, p.gotRoot, p.gotSnapshot, p.gotOutline, underlying.snapshotCalls)
	}
	failure := errors.New("synthetic provider refused scoped outline")
	p.failure = failure
	actual, err = session.Accessibility.Outline(context.Background(), root, snapshotOpts, outlineOpts)
	if !errors.Is(err, failure) || p.calls != 2 || underlying.snapshotCalls != 0 {
		t.Fatalf("provider failure was hidden or retried as unscoped snapshot: err=%v calls=%d fallback=%d", err, p.calls, underlying.snapshotCalls)
	}
}
