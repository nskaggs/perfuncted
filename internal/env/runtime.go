package env

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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

// Has reports whether key is present, even if it is explicitly set to empty.
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
	out.vars["DISPLAY"] = ""
	out.vars["SWAYSOCK"] = ""
	out.vars["HYPRLAND_INSTANCE_SIGNATURE"] = ""
	out.vars["GDK_BACKEND"] = "wayland"
	out.vars["QT_QPA_PLATFORM"] = "wayland"
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
	sort.Strings(keys)
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

// SocketPath resolves the Wayland socket path for the runtime snapshot.
func (r Runtime) SocketPath() string {
	sock := r.Get("WAYLAND_DISPLAY")
	if sock == "" {
		return ""
	}
	if filepath.IsAbs(sock) {
		return sock
	}
	xrd := r.Get("XDG_RUNTIME_DIR")
	if xrd == "" {
		return sock
	}
	return filepath.Join(xrd, sock)
}

func (r Runtime) clone() Runtime {
	out := Runtime{vars: make(map[string]string, len(r.vars))}
	for key, value := range r.vars {
		out.vars[key] = value
	}
	return out
}
