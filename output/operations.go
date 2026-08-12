package output

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("outputs")
}

// SupportedOperations returns the operations supported by the X11 lister.
func (l *X11Lister) SupportedOperations() []string { return supportedOperations() }

// SupportedOperations returns the operations supported by the Wayland lister.
func (l *WaylandLister) SupportedOperations() []string { return supportedOperations() }
