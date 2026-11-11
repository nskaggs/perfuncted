package main

import (
	"bytes"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"io"
	"strings"
	"testing"
)

type infoFailingWriter struct {
	max   int
	count int
}

func (w *infoFailingWriter) Write(p []byte) (int, error) {
	w.count++
	if w.count >= w.max {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestInfoPlainShowsResolvedBackendAndRedactsUnrelatedEnvironment(t *testing.T) {
	session := pftest.New(nil, nil, nil, nil)
	report := infoReport{Target: "synthetic managed session", Compositor: "Wayland", Environment: map[string]string{
		"WAYLAND_DISPLAY": "wayland-synthetic", "DISPLAY": ":99", "XDG_CURRENT_DESKTOP": "GNOME", "XDG_RUNTIME_DIR": "/tmp/synthetic-runtime", "SECRET_TOKEN": "must-not-print",
	}, Capabilities: map[string]capabilityEntry{
		"input":  {Supported: true, Backend: "*input.XTestBackend", Operations: []string{"click", "type"}},
		"screen": {Supported: false, Backend: "", Reason: "compositor capture unauthorized"},
	}}
	var buf bytes.Buffer
	statuses := []perfuncted.CapabilityStatus{{Capability: "input", Available: true}, {Capability: "screen", Available: false}}
	if err := writeInfoEnvironment(&buf, report); err != nil {
		t.Fatal(err)
	}
	if err := writeInfoCapabilities(&buf, statuses, report); err != nil {
		t.Fatal(err)
	}
	result := buf.String()
	for _, wanted := range []string{"synthetic managed session", "Wayland", "wayland-synthetic", "XDG_CURRENT_DESKTOP:", "[✓]", "[ ]", "failure=compositor capture unauthorized", "operations=click,type"} {
		if !strings.Contains(result, wanted) {
			t.Fatalf("info missing required status %q: %s", wanted, result)
		}
	}
	if strings.Contains(result, "must-not-print") || strings.Contains(result, "SECRET_TOKEN") {
		t.Fatalf("info leaked unrelated environment field: %s", result)
	}
	buf.Reset()
	if err := writeInfoPlain(&buf, session, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "WARNING:") || !strings.Contains(buf.String(), "XWayland") {
		t.Fatalf("native Wayland input limitation not explained: %q", buf.String())
	}
}
func TestInfoRenderersPropagateClosedOutputErrors(t *testing.T) {
	report := infoReport{Target: "target", Compositor: "Wayland", Environment: map[string]string{"WAYLAND_DISPLAY": "w1", "DISPLAY": ":1"}, Capabilities: map[string]capabilityEntry{"screen": {Backend: "test", Reason: "unavailable", Supported: false}}}
	statuses := []perfuncted.CapabilityStatus{{Capability: "screen"}}
	for _, tc := range []struct {
		name string
		call func(io.Writer) error
	}{
		{"environment", func(w io.Writer) error { return writeInfoEnvironment(w, report) }},
		{"capabilities", func(w io.Writer) error { return writeInfoCapabilities(w, statuses, report) }},
		{"plain", func(w io.Writer) error { return writeInfoPlain(w, pftest.New(nil, nil, nil, nil), report) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, failAt := range []int{1, 2, 3, 4} {
				writer := &infoFailingWriter{max: failAt}
				err := tc.call(writer)
				if writer.count >= failAt && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("write failure at %d was ignored: err=%v calls=%d", failAt, err, writer.count)
				}
			}
		})
	}
}

func TestInfoWarnsWhenWaylandInputIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		report  infoReport
		warning string
	}{
		{
			name: "Wayland sandbox blocked virtual input",
			report: infoReport{Compositor: "wlroots Wayland", Capabilities: map[string]capabilityEntry{
				"input": {Supported: false, Reason: "no backend available for Wayland desktop"},
			}},
			warning: "--input-backend remote-desktop",
		},
		{
			name: "X11 missing input does not suggest Wayland portal",
			report: infoReport{Compositor: "X11", Capabilities: map[string]capabilityEntry{
				"input": {Supported: false},
			}},
			warning: "",
		},
		{
			name: "native Wayland backend supported",
			report: infoReport{Compositor: "wlroots Wayland", Capabilities: map[string]capabilityEntry{
				"input": {Supported: true, Backend: "*input.WlVirtualBackend"},
			}},
			warning: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inputBackendWarning(tc.report)
			if tc.warning == "" && got != "" {
				t.Fatalf("unexpected backend warning %q", got)
			}
			if tc.warning != "" && !strings.Contains(got, tc.warning) {
				t.Fatalf("warning %q missing instruction %q", got, tc.warning)
			}
		})
	}
}
