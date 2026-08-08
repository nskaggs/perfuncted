package output

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("outputs")
}

func (l *X11Lister) SupportedOperations() []string { return supportedOperations() }

func (l *WaylandLister) SupportedOperations() []string { return supportedOperations() }
