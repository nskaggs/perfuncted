//go:build linux
// +build linux

package clipboard

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

var _ Clipboard = (*GnomeNativeClipboard)(nil)

// GnomeNativeClipboard accesses St.Clipboard through the bundled Shell
// extension, avoiding helper processes and compositor-specific clipboard
// command availability.
type GnomeNativeClipboard struct {
	bridge *gnomebridge.Client
}

// NewGnomeNativeClipboardForRuntime connects to the GNOME-native clipboard.
func NewGnomeNativeClipboardForRuntime(rt env.Runtime) (*GnomeNativeClipboard, error) {
	return NewGnomeNativeClipboardForRuntimeContext(context.Background(), rt)
}

// NewGnomeNativeClipboardForRuntimeContext connects to the GNOME-native
// clipboard while honoring ctx during capability negotiation.
func NewGnomeNativeClipboardForRuntimeContext(ctx context.Context, rt env.Runtime) (*GnomeNativeClipboard, error) {
	ctx = contextutil.Default(ctx)
	bridge, err := gnomebridge.ConnectForCapability(ctx, rt, gnomebridge.CapabilityClipboard)
	if err != nil {
		return nil, fmt.Errorf("clipboard/gnome-native: %w", err)
	}
	return &GnomeNativeClipboard{bridge: bridge}, nil
}

// Get returns clipboard text through GNOME Shell.
func (c *GnomeNativeClipboard) Get(ctx context.Context) (string, error) {
	if c == nil || c.bridge == nil {
		return "", fmt.Errorf("clipboard/gnome-native: backend is not initialised")
	}
	return c.bridge.GetText(ctx)
}

// Set replaces clipboard text through GNOME Shell.
func (c *GnomeNativeClipboard) Set(ctx context.Context, text string) error {
	if c == nil || c.bridge == nil {
		return fmt.Errorf("clipboard/gnome-native: backend is not initialised")
	}
	return c.bridge.SetText(ctx, text)
}

// Close releases the GNOME bridge connection.
func (c *GnomeNativeClipboard) Close() error {
	if c == nil || c.bridge == nil {
		return nil
	}
	return c.bridge.Close()
}
