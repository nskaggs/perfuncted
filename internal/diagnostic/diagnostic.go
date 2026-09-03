// Package diagnostic contains shared, privacy-aware diagnostic report helpers.
package diagnostic

import (
	"strings"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// Probes reports the availability of every diagnostic surface using the same
// runtime snapshot and redacts sensitive values from probe reasons.
func Probes(rt env.Runtime) map[string][]probe.Result {
	environment := rt.EnvList()
	return map[string][]probe.Result{
		"screen": RedactProbeResults(screen.ProbeRuntime(rt), environment),
		"input":  RedactProbeResults(input.ProbeRuntime(rt), environment),
		"window": RedactProbeResults(window.ProbeRuntime(rt), environment),
		"output": RedactProbeResults(output.ProbeRuntime(rt), environment),
	}
}

// RedactProbeResults returns a copy of results with routing values removed
// from human-readable reasons.
func RedactProbeResults(results []probe.Result, environment []string) []probe.Result {
	if len(results) == 0 {
		return nil
	}
	redacted := append([]probe.Result(nil), results...)
	values := EnvironmentMap(environment)
	for i := range redacted {
		for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
			if value := values[key]; value != "" {
				redacted[i].Reason = strings.ReplaceAll(redacted[i].Reason, value, "<set>")
			}
		}
	}
	return redacted
}

// Environment returns the allowlisted environment metadata suitable for a
// diagnostic report. Routing values are represented only by their presence.
func Environment(environment []string) map[string]string {
	all := EnvironmentMap(environment)
	values := make(map[string]string, 6)
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XDG_CURRENT_DESKTOP", "XDG_SESSION_TYPE"} {
		if value := all[key]; value != "" {
			values[key] = value
		}
	}
	for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
		if all[key] != "" {
			values[key] = "<set>"
		}
	}
	return values
}

// EnvironmentMap parses KEY=VALUE entries into a map.
func EnvironmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
