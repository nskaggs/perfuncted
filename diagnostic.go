package perfuncted

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// FailureBundleOptions controls an opt-in diagnostic capture.
//
// A bundle can contain screenshots, window titles, process IDs, and other
// desktop state. Callers should treat the directory as sensitive and should
// not upload it without explicit user consent.
type FailureBundleOptions struct {
	// Directory is the directory to create or update. When empty, a temporary
	// directory is created and its path is returned by CaptureFailureBundle.
	Directory string
	// Operation identifies the operation or test that failed.
	Operation string
	// Error is the failure that prompted the capture, when available.
	Error error
	// Metadata contains caller-supplied context to include in manifest.json.
	Metadata map[string]string
}

// CaptureFailureBundle collects best-effort diagnostic artifacts into a
// directory and returns its path. Each artifact is attempted independently;
// the returned error reports collection failures after the successful files
// have been written.
func (s *Session) CaptureFailureBundle(ctx context.Context, options FailureBundleOptions) (string, error) {
	if s == nil {
		return "", ErrNilSession
	}
	if ctx == nil {
		return "", fmt.Errorf("perfuncted: capture failure bundle: %w: nil context", ErrInvalidArgument)
	}

	directory := options.Directory
	if directory == "" {
		var err error
		directory, err = os.MkdirTemp("", "perfuncted-failure-")
		if err != nil {
			return "", fmt.Errorf("perfuncted: create failure bundle directory: %w", err)
		}
	} else if err := os.MkdirAll(directory, 0o750); err != nil {
		return "", fmt.Errorf("perfuncted: create failure bundle directory %q: %w", directory, err)
	}

	manifest := failureManifest{
		FormatVersion:  1,
		CapturedAt:     time.Now().UTC(),
		Operation:      options.Operation,
		Target:         string(s.Target().Kind()),
		Compositor:     compositor.DetectRuntime(s.env).String(),
		Environment:    diagnosticEnvironment(s.env.EnvList()),
		Build:          currentBuildInfo(),
		Metadata:       copyStringMap(options.Metadata),
		Capabilities:   diagnosticCapabilities(s.Capabilities()),
		Probes:         diagnosticProbes(s.env), //nolint:contextcheck // probe APIs are synchronous and do not accept a context.
		ArtifactErrors: make(map[string]string),
	}
	if options.Error != nil {
		manifest.Error = options.Error.Error()
	}

	artifactErrors := s.captureFailureArtifacts(ctx, directory, &manifest)

	manifest.Artifacts = uniqueSortedStrings(manifest.Artifacts)
	manifest.ArtifactErrors = copyStringMap(manifest.ArtifactErrors)
	if err := writeJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		artifactErrors = errors.Join(artifactErrors, fmt.Errorf("manifest.json: %w", err))
	}
	return directory, artifactErrors
}

type failureArtifactCollector struct {
	manifest *failureManifest
	errors   []error
}

func (c *failureArtifactCollector) capture(name string, fn func() error) {
	if err := fn(); err != nil {
		c.manifest.ArtifactErrors[name] = err.Error()
		c.errors = append(c.errors, fmt.Errorf("%s: %w", name, err))
		return
	}
	c.manifest.Artifacts = append(c.manifest.Artifacts, name)
}

func (s *Session) captureFailureArtifacts(ctx context.Context, directory string, manifest *failureManifest) error {
	collector := failureArtifactCollector{manifest: manifest}
	collector.capture("screenshot.png", func() error { return s.captureScreenshot(ctx, directory) })
	collector.capture("windows.json", func() error { return s.captureWindows(ctx, directory) })
	collector.capture("active-window.json", func() error { return s.captureActiveWindow(ctx, directory) })
	collector.capture("outputs.json", func() error { return s.captureOutputs(ctx, directory) })
	collector.capture("trace.txt", func() error { return s.captureTrace(directory) })
	return errors.Join(collector.errors...)
}

func (s *Session) captureScreenshot(ctx context.Context, directory string) error {
	if s.Screen == nil || util.IsNil(s.Screen.backend) {
		return errors.New("screen capability is not configured")
	}
	img, err := s.Screen.backend.Grab(ctx, image.Rectangle{})
	if err != nil {
		return err
	}
	if img == nil {
		return errors.New("screen backend returned a nil image")
	}
	return writePNG(filepath.Join(directory, "screenshot.png"), img)
}

func (s *Session) captureWindows(ctx context.Context, directory string) error {
	if s.Windows == nil || util.IsNil(s.Windows.backend) {
		return errors.New("window capability is not configured")
	}
	windows, err := s.Windows.backend.List(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "windows.json"), windows)
}

func (s *Session) captureActiveWindow(ctx context.Context, directory string) error {
	if s.Windows == nil || util.IsNil(s.Windows.backend) {
		return errors.New("window capability is not configured")
	}
	title, err := s.Windows.backend.ActiveTitle(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "active-window.json"), struct {
		Title string `json:"title"`
	}{Title: title})
}

func (s *Session) captureOutputs(ctx context.Context, directory string) error {
	if s.Outputs == nil || util.IsNil(s.Outputs.backend) {
		return errors.New("output capability is not configured")
	}
	outputs, err := s.Outputs.backend.List(ctx)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(directory, "outputs.json"), outputs)
}

func (s *Session) captureTrace(directory string) error {
	trace := strings.Join(s.tracer.recent(), "\n")
	if trace != "" {
		trace += "\n"
	}
	return os.WriteFile(filepath.Join(directory, "trace.txt"), []byte(trace), 0o600)
}

type failureManifest struct {
	FormatVersion  int                       `json:"format_version"`
	CapturedAt     time.Time                 `json:"captured_at"`
	Operation      string                    `json:"operation,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Target         string                    `json:"target"`
	Compositor     string                    `json:"compositor"`
	Environment    map[string]string         `json:"environment"`
	Build          diagnosticBuildInfo       `json:"build"`
	Metadata       map[string]string         `json:"metadata,omitempty"`
	Capabilities   []diagnosticCapability    `json:"capabilities"`
	Probes         map[string][]probe.Result `json:"probes"`
	Artifacts      []string                  `json:"artifacts"`
	ArtifactErrors map[string]string         `json:"artifact_errors,omitempty"`
}

type diagnosticCapability struct {
	Capability string   `json:"capability"`
	Requested  bool     `json:"requested"`
	Required   bool     `json:"required"`
	Available  bool     `json:"available"`
	Backend    string   `json:"backend,omitempty"`
	Operations []string `json:"operations,omitempty"`
	Failure    string   `json:"failure,omitempty"`
}

type diagnosticBuildInfo struct {
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Module        string `json:"module,omitempty"`
	ModuleVersion string `json:"module_version,omitempty"`
	Revision      string `json:"revision,omitempty"`
	RevisionTime  string `json:"revision_time,omitempty"`
	Modified      bool   `json:"modified,omitempty"`
}

func diagnosticCapabilities(statuses []CapabilityStatus) []diagnosticCapability {
	out := make([]diagnosticCapability, 0, len(statuses))
	for _, status := range statuses {
		item := diagnosticCapability{
			Capability: string(status.Capability),
			Requested:  status.Requested,
			Required:   status.Required,
			Available:  status.Available,
			Backend:    status.Backend,
			Operations: append([]string(nil), status.Operations...),
		}
		if status.Failure != nil {
			item.Failure = status.Failure.Error()
		}
		out = append(out, item)
	}
	return out
}

func diagnosticProbes(rt env.Runtime) map[string][]probe.Result {
	environment := rt.EnvList()
	return map[string][]probe.Result{
		"screen": redactProbeResults(screen.ProbeRuntime(rt), environment),
		"input":  redactProbeResults(input.ProbeRuntime(rt), environment),
		"window": redactProbeResults(window.ProbeRuntime(rt), environment),
		"output": redactProbeResults(output.ProbeRuntime(rt), environment),
	}
}

func redactProbeResults(results []probe.Result, environment []string) []probe.Result {
	if len(results) == 0 {
		return nil
	}
	redacted := append([]probe.Result(nil), results...)
	values := environmentMap(environment)
	for i := range redacted {
		for _, key := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR"} {
			if value := values[key]; value != "" {
				redacted[i].Reason = strings.ReplaceAll(redacted[i].Reason, value, "<set>")
			}
		}
	}
	return redacted
}

func currentBuildInfo() diagnosticBuildInfo {
	info := diagnosticBuildInfo{GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	build, ok := debug.ReadBuildInfo()
	if !ok || build == nil {
		return info
	}
	info.Module = build.Path
	info.ModuleVersion = build.Main.Version
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			info.Revision = setting.Value
		case "vcs.time":
			info.RevisionTime = setting.Value
		case "vcs.modified":
			info.Modified, _ = strconv.ParseBool(setting.Value)
		}
	}
	return info
}

func diagnosticEnvironment(environment []string) map[string]string {
	all := environmentMap(environment)
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

func environmentMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func writePNG(path string, img image.Image) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func copyStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	// The artifact set is small; insertion sort avoids another dependency.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
