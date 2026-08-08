package screen

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("screen")
}

func (b *X11Backend) SupportedOperations() []string { return supportedOperations() }

func (b *KWinShotBackend) SupportedOperations() []string { return supportedOperations() }

func (b *PortalDBusBackend) SupportedOperations() []string { return supportedOperations() }

func (b *GnomeShellScreenshotBackend) SupportedOperations() []string {
	return supportedOperations()
}

func (b *ExtCaptureBackend) SupportedOperations() []string { return supportedOperations() }

func (b *WlrScreencopyBackend) SupportedOperations() []string {
	return supportedOperations()
}
