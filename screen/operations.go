package screen

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("screen")
}

// SupportedOperations returns the operations supported by the X11 backend.
func (b *X11Backend) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations returns the operations supported by the KWin backend.
func (b *KWinShotBackend) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations returns the operations supported by the portal backend.
func (b *PortalDBusBackend) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations returns the operations supported by the GNOME backend.
func (b *GnomeShellScreenshotBackend) SupportedOperations() []string {
	return supportedOperations()
}

// SupportedOperations returns the operations supported by the native GNOME
// bridge backend.
func (b *GnomeNativeScreenBackend) SupportedOperations() []string {
	return supportedOperations()
}

// SupportedOperations returns the operations supported by the ext-capture backend.
func (b *ExtCaptureBackend) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations returns the operations supported by the wlr backend.
func (b *WlrScreencopyBackend) SupportedOperations() []string {
	return supportedOperations()
}
