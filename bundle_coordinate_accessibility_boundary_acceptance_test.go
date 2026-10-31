package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/input"
)

type authoritativeCoordinateBackend struct {
	*closeCountingInput
	report  input.CoordinateSpaceInfo
	failure error
	calls   int
}

func (b *authoritativeCoordinateBackend) PointerCoordinateSpace(ctx context.Context) (input.CoordinateSpaceInfo, error) {
	b.calls++
	if err := ctx.Err(); err != nil {
		return input.CoordinateSpaceInfo{}, err
	}
	return b.report, b.failure
}

func TestInputBundleCoordinateSpaceRequiresAuthoritativeValidBackendProof(t *testing.T) {
	physical := input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5}
	backend := &authoritativeCoordinateBackend{closeCountingInput: &closeCountingInput{}, report: physical}
	session := NewSessionForTesting(nil, backend, nil, nil, nil)
	defer session.Close()
	got, err := session.Input.PointerCoordinateSpace(context.Background())
	if err != nil || got != physical || backend.calls != 1 {
		t.Fatalf("authoritative coordinate report lost: %+v err=%v calls=%d", got, err, backend.calls)
	}
	backend.report = input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 2, ScaleY: 1}
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); err == nil {
		t.Fatal("non-unit logical scale accepted")
	}
	failure := errors.New("synthetic coordinate discovery failed")
	backend.failure = failure
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("backend error lost: %v", err)
	}
	backend.failure = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Input.PointerCoordinateSpace(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); err == nil {
		t.Fatal("closed session disclosed coordinate scale")
	}
	withoutMetadata := NewSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer withoutMetadata.Close()
	if _, err := withoutMetadata.Input.PointerCoordinateSpace(context.Background()); !errors.Is(err, input.ErrNotSupported) {
		t.Fatalf("unproven coordinate space was not rejected: %v", err)
	}
}

func TestAccessibilityWindowDelegatesExplicitScopeAndPreservesFailure(t *testing.T) {
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Synthetic Editor"}}}, gen: 7}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	ctx := context.Background()
	scope, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "synthetic-editor-window"})
	if err != nil || scope.WindowID != "synthetic-editor-window" || scope.Generation != 7 || len(fake.resolvedTargets) != 1 || fake.resolvedTargets[0].ID != "synthetic-editor-window" {
		t.Fatalf("incorrect window correlation scope=%+v err=%v targets=%+v", scope, err, fake.resolvedTargets)
	}
	if _, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "missing"}); !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("missing window error lost: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "synthetic-editor-window"}); err == nil {
		t.Fatal("closed accessibility session resolved stale window")
	}
}

func TestNilAccessibilityBundleClosesRejectedBackendWithoutLeaking(t *testing.T) {
	var absent *AccessibilityBundle
	candidate := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
	absent.installBackend(candidate)
	if !candidate.closed {
		t.Fatal("rejected backend was not closed")
	}
	if err := absent.close(); err != nil {
		t.Fatal(err)
	}
	if err := absent.checkAvailable("window-root"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil accessibility bundle returned %v", err)
	}
}
