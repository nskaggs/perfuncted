package input

import "github.com/nskaggs/perfuncted/internal/capability"

func supportedOperations(pointerLocation, coordinateSpace bool) []string {
	var exclude []string
	if !pointerLocation {
		exclude = append(exclude, "pointer-location")
	}
	if !coordinateSpace {
		exclude = append(exclude, "pointer-coordinate-space")
	}
	return capability.Operations("input", exclude...)
}

// SupportedOperations reports operations that are executable by the selected
// backend. It is consumed by the parent Session when publishing capability
// status; it is not a second dispatch table.
func (b *XTestBackend) SupportedOperations() []string {
	return supportedOperations(true, false)
}

// SupportedOperations reports operations that are executable by the selected
// backend. uinput cannot query the current pointer location.
func (b *UinputBackend) SupportedOperations() []string {
	return supportedOperations(false, false)
}

// SupportedOperations reports operations that are executable by the selected
// backend. The virtual Wayland backend cannot query the current pointer.
func (b *WlVirtualBackend) SupportedOperations() []string {
	return supportedOperations(false, true)
}

// SupportedOperations reports the input surface provided by the GNOME Shell
// virtual-device backend. Mutter does not expose a completion barrier for
// virtual-input tasks, so this backend deliberately omits sync.
func (b *GnomeNativeBackend) SupportedOperations() []string {
	return capability.Operations("input", "sync")
}
