package env

import (
	"os"
	"slices"
	"strings"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Runtime is a snapshot of the environment used to route automation requests
// to a specific desktop session.
type Runtime struct {
	vars map[string]string
}

// Current captures the current process environment.
func Current() Runtime {
	return FromEnviron(os.Environ())
}

// FromEnviron parses env vars in KEY=VALUE form into a Runtime snapshot.
func FromEnviron(values []string) Runtime {
	vars := make(map[string]string, len(values))
	for _, kv := range values {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			vars[kv[:i]] = kv[i+1:]
		}
	}
	return Runtime{vars: vars}
}

// Get returns the value for key, or the empty string when unset.
func (r Runtime) Get(key string) string {
	if r.vars == nil {
		return ""
	}
	return r.vars[key]
}

// Lookup returns the value for key and whether it was present in the snapshot.
func (r Runtime) Lookup(key string) (string, bool) {
	if r.vars == nil {
		return "", false
	}
	v, ok := r.vars[key]
	return v, ok
}

// Has reports whether key is present in the runtime snapshot.
func (r Runtime) Has(key string) bool {
	if r.vars == nil {
		return false
	}
	_, ok := r.vars[key]
	return ok
}

// With returns a copy of r with key set to value.
func (r Runtime) With(key, value string) Runtime {
	out := r.clone()
	out.vars[key] = value
	return out
}

// Without returns a copy of r with the provided keys removed.
func (r Runtime) Without(keys ...string) Runtime {
	out := r.clone()
	for _, key := range keys {
		delete(out.vars, key)
	}
	return out
}

// WithSession overlays session-routing variables and clears conflicting host
// desktop routing that would otherwise leak actions outside the target session.
func (r Runtime) WithSession(xdgRuntimeDir, waylandDisplay, dbusAddr string) Runtime {
	out := r.clone()
	out.vars["XDG_RUNTIME_DIR"] = xdgRuntimeDir
	out.vars["WAYLAND_DISPLAY"] = waylandDisplay
	out.vars["DBUS_SESSION_BUS_ADDRESS"] = dbusAddr
	// AT_SPI_BUS is an X root-window property, not the managed accessibility
	// bus address. Never let host AT-SPI routing or toolkit bridge overrides
	// leak into an isolated session: the session layer publishes its address
	// only after querying the managed org.a11y.Bus service.
	for _, key := range []string{
		"AT_SPI_BUS",
		"ATSPI_BUS_ADDRESS",
		"AT_SPI_BUS_ADDRESS",
		"GTK_MODULES",
		"GTK_A11Y",
		"GNOME_ACCESSIBILITY",
		"QT_ACCESSIBILITY",
		"QT_LINUX_ACCESSIBILITY_ALWAYS_ON",
		"XDG_SESSION_TYPE",
		"DISPLAY",
		"SWAYSOCK",
		"HYPRLAND_INSTANCE_SIGNATURE",
		"GDK_BACKEND",
		"QT_QPA_PLATFORM",
	} {
		delete(out.vars, key)
	}
	// Clear session-type and toolkit env so backends are selected by
	// the caller.
	return out
}

// WithAccessibilityBus publishes the AT-SPI bus selected by a managed
// session. An empty address removes the override so clients discover AT-SPI
// through the session bus without inheriting a host-session address.
func (r Runtime) WithAccessibilityBus(addr string) Runtime {
	out := r.clone()
	delete(out.vars, "AT_SPI_BUS")
	delete(out.vars, "AT_SPI_BUS_ADDRESS")
	if strings.TrimSpace(addr) == "" {
		delete(out.vars, "ATSPI_BUS_ADDRESS")
	} else {
		out.vars["ATSPI_BUS_ADDRESS"] = strings.TrimSpace(addr)
	}
	return out
}

// EnvList returns the runtime as a deterministic env slice suitable for
// exec.Cmd.Env.
func (r Runtime) EnvList() []string {
	if len(r.vars) == 0 {
		// Return an empty slice (not nil) to represent an empty environment
		// for exec.Cmd.Env. Nil would mean "inherit parent env".
		return []string{}
	}
	keys := make([]string, 0, len(r.vars))
	for key := range r.vars {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+r.vars[key])
	}
	return out
}

// Display returns the DISPLAY value from the runtime snapshot.
func (r Runtime) Display() string {
	return r.Get("DISPLAY")
}

// SocketPath resolves the Wayland socket path for the runtime snapshot, or
// returns the empty string when the socket cannot be resolved.
func (r Runtime) SocketPath() string {
	return wl.ResolveSocketPath(r.Get("WAYLAND_DISPLAY"), r.Get("XDG_RUNTIME_DIR"))
}

func (r Runtime) clone() Runtime {
	out := Runtime{vars: make(map[string]string, len(r.vars))}
	for key, value := range r.vars {
		out.vars[key] = value
	}
	return out
}
