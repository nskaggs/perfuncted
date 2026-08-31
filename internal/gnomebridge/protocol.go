// Package gnomebridge contains the private, versioned D-Bus bridge used by
// the GNOME-native capability backends.
package gnomebridge

const (
	// BusName is the well-known session-bus name owned by the bundled Shell
	// extension.
	BusName = "io.github.nskaggs.perfuncted.Gnome1"
	// ObjectPath is the single object exported by the extension.
	ObjectPath = "/io/github/nskaggs/perfuncted/Gnome1"

	CoreInterface      = BusName + ".Core"
	WindowsInterface   = BusName + ".Windows"
	ScreenInterface    = BusName + ".Screen"
	InputInterface     = BusName + ".Input"
	ClipboardInterface = BusName + ".Clipboard"

	// ProtocolVersion is deliberately independent of the perfuncted release
	// version. A bridge may be upgraded without requiring a matching binary.
	ProtocolVersion  uint32 = 1
	ExtensionVersion        = "1"
)

// Capability names are returned by Core.GetCapabilities and used to gate
// calls made by each native adapter.
const (
	CapabilityWindows   = "windows"
	CapabilityScreen    = "screen"
	CapabilityInput     = "input"
	CapabilityClipboard = "clipboard"
)

// WindowInfo is the D-Bus-native representation of window.Info. Keep the
// field order in sync with windowInfoSignature in the extension and client.
type WindowInfo struct {
	ID         string
	Title      string
	AppID      string
	Class      string
	PID        int32
	X          int32
	Y          int32
	Width      int32
	Height     int32
	Active     bool
	Minimized  bool
	Maximized  bool
	Fullscreen bool
}

// ScreenCapture describes the logical rectangle and physical PNG dimensions
// produced by a Shell.Screenshot operation. GNOME uses logical screen
// coordinates for the request, but may scale the encoded image for the
// monitor view that contains it.
type ScreenCapture struct {
	X           int32
	Y           int32
	Width       int32
	Height      int32
	PixelWidth  int32
	PixelHeight int32
	Scale       float64
}

// WindowEventKind identifies a lifecycle or focus notification from GNOME
// Shell.
type WindowEventKind string

const (
	WindowAddedEvent   WindowEventKind = "window-added"
	WindowRemovedEvent WindowEventKind = "window-removed"
	WindowChangedEvent WindowEventKind = "window-changed"
	FocusChangedEvent  WindowEventKind = "focus-changed"
)

// WindowEvent is the typed representation of a Windows interface signal.
// Added and changed events carry Window; removed and focus events carry ID.
type WindowEvent struct {
	Kind   WindowEventKind
	Window WindowInfo
	ID     string
}
