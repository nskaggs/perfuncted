package input

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations(pointerLocation bool) []string {
	if pointerLocation {
		return capability.Operations("input")
	}
	return capability.Operations("input", "pointer-location")
}

// SupportedOperations reports operations that are executable by the selected
// backend. It is consumed by the parent Session when publishing capability
// status; it is not a second dispatch table.
func (b *XTestBackend) SupportedOperations() []string {
	return supportedOperations(true)
}

// SupportedOperations reports operations that are executable by the selected
// backend. uinput cannot query the current pointer location.
func (b *UinputBackend) SupportedOperations() []string {
	return supportedOperations(false)
}

// SupportedOperations reports operations that are executable by the selected
// backend. The virtual Wayland backend cannot query the current pointer.
func (b *WlVirtualBackend) SupportedOperations() []string {
	return supportedOperations(false)
}

// SupportedOperations reports the input surface provided by the GNOME Shell
// virtual-device backend. Mutter does not expose a completion barrier for
// virtual-input tasks, so this backend deliberately omits sync.
func (b *GnomeNativeBackend) SupportedOperations() []string {
	return capability.Operations("input", "sync")
}
