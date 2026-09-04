package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/util"
)

var openAccessibility = accessibility.OpenRuntime

// AccessibilityBundle exposes read-only AT-SPI queries for the active session.
// It is optional because many headless or minimal desktop sessions do not run
// an accessibility bus.
type AccessibilityBundle struct {
	backend accessibility.Backend
	bundleBase
}

// NewAccessibilityBundle binds a deterministic accessibility backend. It is
// useful for adapters and tests that need to exercise semantic workflows
// without a live AT-SPI bus.
func NewAccessibilityBundle(backend accessibility.Backend) *AccessibilityBundle {
	return &AccessibilityBundle{backend: backend, bundleBase: bundleBase{capability: CapabilityAccessibility}}
}

func (b *AccessibilityBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.bundleBase.checkAvailable(operation, !util.IsNil(b.backend))
}

func (b *AccessibilityBundle) close() error {
	if b == nil || util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

// Applications lists application roots currently registered with AT-SPI.
func (b *AccessibilityBundle) Applications(ctx context.Context) ([]accessibility.Application, error) {
	if err := b.checkAvailable("applications"); err != nil {
		return nil, err
	}
	apps, err := b.backend.Applications(ctx)
	return apps, b.operationError("applications", err)
}

// Snapshot returns a bounded accessibility tree. A zero root selects the
// virtual desktop root.
func (b *AccessibilityBundle) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	if err := b.checkAvailable("snapshot"); err != nil {
		return accessibility.Snapshot{}, err
	}
	snapshot, err := b.backend.Snapshot(ctx, root, opts)
	return snapshot, b.operationError("snapshot", err)
}

// Find returns nodes matching a bounded case-insensitive query.
func (b *AccessibilityBundle) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	if err := b.checkAvailable("find"); err != nil {
		return nil, err
	}
	nodes, err := b.backend.Find(ctx, root, query, opts)
	return nodes, b.operationError("find", err)
}

// Focused returns the currently focused accessible object.
func (b *AccessibilityBundle) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	if err := b.checkAvailable("focused"); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.backend.Focused(ctx, opts)
	return node, b.operationError("focused", err)
}

// AtPoint returns the accessible object at a screen coordinate.
func (b *AccessibilityBundle) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	if err := b.checkAvailable("at-point"); err != nil {
		return accessibility.Node{}, err
	}
	node, err := b.backend.AtPoint(ctx, x, y)
	return node, b.operationError("at-point", err)
}

// FindApplication selects a single application by accessible name, PID, or
// AT-SPI bus name when the runtime backend exposes process ownership.
func (b *AccessibilityBundle) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	if err := b.checkAvailable("find-application"); err != nil {
		return accessibility.Application{}, err
	}
	finder, ok := b.backend.(accessibility.ApplicationFinder)
	if !ok {
		return accessibility.Application{}, b.operationError("find-application", accessibility.ErrUnsupported)
	}
	app, err := finder.FindApplication(ctx, filter)
	return app, b.operationError("find-application", err)
}

// Events returns the bounded AT-SPI invalidation stream when supported.
func (b *AccessibilityBundle) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.backend.(accessibility.EventSource)
	if !ok {
		return nil, b.operationError("events", accessibility.ErrUnsupported)
	}
	stream, err := source.Events(ctx, opts)
	return stream, b.operationError("events", err)
}

// Generation returns the current accessibility invalidation generation.
func (b *AccessibilityBundle) Generation() uint64 {
	if b == nil || util.IsNil(b.backend) {
		return 0
	}
	if source, ok := b.backend.(accessibility.GenerationSource); ok {
		return source.Generation()
	}
	return 0
}

// Invalidate drops cached accessibility observations when a caller observes a
// state change through another channel.
func (b *AccessibilityBundle) Invalidate(id accessibility.NodeID) {
	if b == nil || util.IsNil(b.backend) {
		return
	}
	if source, ok := b.backend.(accessibility.GenerationSource); ok {
		source.Invalidate(id)
	}
}
