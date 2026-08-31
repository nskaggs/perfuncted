//go:build linux
// +build linux

package clipboard

import (
	"context"
	"fmt"

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
	bridge, err := gnomebridge.ConnectRuntime(context.Background(), rt)
	if err != nil {
		return nil, err
	}
	if !bridge.HasCapability(gnomebridge.CapabilityClipboard) {
		_ = bridge.Close()
		return nil, fmt.Errorf("clipboard/gnome-native: bridge does not advertise clipboard capability")
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
