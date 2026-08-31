package clipboard

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("clipboard")
}

func (c *extCmdClipboard) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations reports the operations supported by the GNOME backend.
func (c *GnomeNativeClipboard) SupportedOperations() []string { return supportedOperations() }
