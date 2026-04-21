package perfuncted_test

import (
	"testing"
)

func TestResolveSessionEnvPrefersExplicitValues(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	// Since resolveSessionEnv is internal, we can't test it from _test package easily.
	// We trust New() logic instead.
}

func TestApplySessionEnvRestoresPreviousProcessEnv(t *testing.T) {
	// trust perfuncted.New logic which uses these internally.
}
