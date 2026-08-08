package clipboard

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations() []string {
	return capability.Operations("clipboard")
}

func (c *extCmdClipboard) SupportedOperations() []string { return supportedOperations() }
