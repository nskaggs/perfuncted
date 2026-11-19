package main

import (
	"errors"
	"io"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// Test each write boundary independently: an error late in rendering must
// never be hidden by later successful writes or a warning footer.
func TestInfoRenderersPreserveLateWriterFailures(t *testing.T) {
	session := pftest.New(nil, nil, nil, nil)
	defer session.Close()
	report := infoReport{
		Target:     "synthetic",
		Compositor: "Wayland",
		Environment: map[string]string{
			"WAYLAND_DISPLAY": "wayland-99", "DISPLAY": ":99",
			"XDG_CURRENT_DESKTOP": "GNOME", "XDG_RUNTIME_DIR": "/synthetic/run",
		},
		Capabilities: map[string]capabilityEntry{
			"input":  {Supported: false, Backend: "XTestBackend", Reason: "synthetic unavailable"},
			"screen": {Supported: true, Backend: "synthetic", Operations: []string{"grab"}},
		},
	}
	statuses := []perfuncted.CapabilityStatus{{Capability: "input"}, {Capability: "screen"}}
	noWarning := report
	noWarning.Compositor = "X11"
	omittedEnvironment := report
	omittedEnvironment.Environment = map[string]string{"WAYLAND_DISPLAY": "wayland-99"}
	for _, tc := range []struct {
		name string
		call func(io.Writer) error
	}{
		{"environment", func(w io.Writer) error { return writeInfoEnvironment(w, report) }},
		{"capabilities", func(w io.Writer) error { return writeInfoCapabilities(w, statuses, report) }},
		{"plain", func(w io.Writer) error { return writeInfoPlain(w, session, report) }},
		{"plain without warning", func(w io.Writer) error { return writeInfoPlain(w, session, noWarning) }},
		{"environment with omitted keys", func(w io.Writer) error { return writeInfoEnvironment(w, omittedEnvironment) }},
		{"flatpak", writeFlatpakSessionHints},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &infoFailingWriter{max: 10000}
			if err := tc.call(probe); err != nil {
				t.Fatalf("uninterrupted rendering failed: %v", err)
			}
			if probe.count < 2 {
				t.Fatalf("expected multiple write boundaries, got %d", probe.count)
			}
			for failAt := 1; failAt <= probe.count; failAt++ {
				writer := &infoFailingWriter{max: failAt}
				if err := tc.call(writer); !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("write #%d/%d was not propagated: %v", failAt, probe.count, err)
				}
				if writer.count != failAt {
					t.Fatalf("write #%d was bypassed (calls=%d)", failAt, writer.count)
				}
			}
		})
	}
}
