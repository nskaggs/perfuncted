package perfuncted

import (
	"context"
	"strings"

	"github.com/nskaggs/perfuncted/accessibility"
)

// resolveManagedWindow selects exactly one managed window by native ID and/or
// exact title. It is the single authority for compositor-window selection
// across WindowRoot, FindApplication, and CLI scope resolution. The operation
// label is caller-owned so error messages reflect the actual entry point.
func (b *AccessibilityBundle) resolveManagedWindow(ctx context.Context, windowID, windowTitle, operation string) (*Window, error) {
	if b == nil {
		return nil, ErrNilSession
	}
	if b.session == nil || b.session.Windows == nil {
		return nil, b.operationError(operation, accessibility.ErrUnsupported)
	}
	windows, err := b.session.Windows.List(ctx, WindowMatch{})
	if err != nil {
		return nil, b.operationError(operation, err)
	}
	wantID, wantTitle := strings.TrimSpace(windowID), strings.TrimSpace(windowTitle)
	var selected *Window
	for _, candidate := range windows {
		candidate.mu.RLock()
		info := candidate.snapshot
		candidate.mu.RUnlock()
		if wantID != "" && candidate.ID().String() != wantID {
			continue
		}
		if wantTitle != "" && info.Title != wantTitle {
			continue
		}
		if selected != nil {
			return nil, b.operationError(operation, accessibility.ErrAmbiguous)
		}
		selected = candidate
	}
	if selected == nil {
		return nil, b.operationError(operation, accessibility.ErrNotFound)
	}
	return selected, nil
}

// windowTargetFor converts an authoritative managed window handle into the
// AT-SPI correlation target. PID, title, geometry, and active state are the
// correlation evidence; PIDHint carries a caller-supplied PID only when the
// compositor did not report one.
func windowTargetFor(selected *Window, pidHint int32) accessibility.WindowTarget {
	selected.mu.RLock()
	info := selected.snapshot
	selected.mu.RUnlock()
	target := accessibility.WindowTarget{ID: selected.ID().String(), Title: info.Title, PID: info.PID, AppID: info.AppID, Bounds: accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H}, Active: info.Active}
	if target.PID == 0 {
		target.PIDHint = pidHint
	}
	return target
}
