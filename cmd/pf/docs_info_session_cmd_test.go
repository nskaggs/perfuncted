package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/spf13/cobra"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Diagnostic output must reveal presence, not the value of a secret-bearing setting.
func TestDiagnosticValueRedactsNonemptySettings(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"secret-token", "<set>"},
		{" ", "<set>"},
		{"0", "<set>"},
	} {
		if got := diagnosticValue(tc.input); got != tc.want {
			t.Errorf("diagnosticValue(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Documentation generation must propagate a file collision instead of
// reporting success or replacing an unrelated directory at its output path.
func TestDocsGenerationRefusesExistingDirectoryAtMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "pf_docs.md")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(blocked, "keep")
	if err := os.WriteFile(sentinel, []byte("unrelated data"), 0600); err != nil {
		t.Fatal(err)
	}
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	root.SetArgs([]string{"docs", "--dir", dir})
	if err := root.Execute(); err == nil {
		t.Fatal("docs generation succeeded despite a directory collision")
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || strings.TrimSpace(string(content)) != "unrelated data" {
		t.Fatalf("failed generation damaged existing output: %q %v", content, err)
	}
}

func TestDocumentationCLIWritesActualMarkdownAndRejectsInvalidDestination(t *testing.T) {
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	target := filepath.Join(t.TempDir(), "generated")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"docs", "--dir", target})
	if err := root.Execute(); err != nil {
		t.Fatalf("generate documentation: %v", err)
	}
	generated, err := os.ReadFile(filepath.Join(target, "pf_docs.md"))
	if err != nil {
		t.Fatalf("documentation not emitted: %v", err)
	}
	if !strings.Contains(string(generated), "--dir") || !strings.Contains(out.String(), target) {
		t.Fatalf("missing documented option or destination: %q / %q", generated, out.String())
	}
	blocker := filepath.Join(t.TempDir(), "file-not-directory")
	if err := os.WriteFile(blocker, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"docs", "--dir", filepath.Join(blocker, "subdir")})
	if err := root.Execute(); err == nil {
		t.Fatal("documentation wrote beneath a regular file")
	}
	if got, err := os.ReadFile(blocker); err != nil || string(got) != "safe" {
		t.Fatalf("failed generation mutated parent: %q, %v", got, err)
	}
}

func TestDocumentationAndVersionWritersPropagateFailureAtEveryStage(t *testing.T) {
	root := &cobra.Command{Use: "pf"}
	root.AddCommand(docsCmd(root))
	target := filepath.Join(t.TempDir(), "docs")
	root.SetArgs([]string{"docs", "--dir", target})
	root.SetOut(scriptBrokenWriter{})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "synthetic output failure") {
		t.Fatalf("documentation report swallowed output failure: %v", err)
	}
	for stage := 1; stage <= 4; stage++ {
		version := versionCmd()
		output := &infoFailingWriter{max: stage}
		version.SetOut(output)
		if err := version.Execute(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("version report stage %d: %v", stage, err)
		}
	}
}

func TestInfoAndSessionTypeCLIExposeSyntheticSessionWithoutDesktop(t *testing.T) {
	session := pftest.New(nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	opens := 0
	open := func(ctx context.Context) (*perfuncted.Session, error) { opens++; return session, nil }
	info := infoCmd(open)
	var out bytes.Buffer
	info.SetOut(&out)
	info.SetArgs([]string{"--output", "json"})
	if err := info.Execute(); err != nil {
		t.Fatalf("synthetic info JSON: %v", err)
	}
	if !strings.Contains(out.String(), "\"capabilities\"") || opens != 1 {
		t.Fatalf("info did not report synthetic capabilities: opens=%d out=%s", opens, out.String())
	}
	info = infoCmd(open)
	info.SetArgs([]string{"--output", "invalid"})
	if err := info.Execute(); err == nil || opens != 1 {
		t.Fatalf("invalid format opened session or succeeded: opens=%d err=%v", opens, err)
	}
	fail := errors.New("synthetic opener rejected environment")
	info = infoCmd(func(context.Context) (*perfuncted.Session, error) { return nil, fail })
	if err := info.Execute(); !errors.Is(err, fail) {
		t.Fatalf("opener failure swallowed: %v", err)
	}
	typeCommand := sessionTypeCmd()
	typeCommand.SetOut(&out)
	if err := typeCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "session:") {
		t.Fatalf("session type did not render: %s", out.String())
	}
	for stage := 1; stage <= 4; stage++ {
		cmd := sessionTypeCmd()
		cmd.SetOut(&infoFailingWriter{max: stage})
		if err := cmd.Execute(); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("session type report stage %d swallowed error: %v", stage, err)
		}
	}
}

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

// The default user-facing info command must render the capability report,
// and a disconnected consumer must not be treated as a successful report.
func TestInfoDefaultPlainCommandReportsCapabilitiesAndWriteFailures(t *testing.T) {
	t.Run("default plain output", func(t *testing.T) {
		opens := 0
		cmd := infoCmd(func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(nil, nil, nil, nil), nil
		})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("default info command: %v", err)
		}
		if opens != 1 || !strings.Contains(out.String(), "Environment") || !strings.Contains(out.String(), "Capabilities") {
			t.Fatalf("default info lost plain sections or opened %d times: %q", opens, out.String())
		}
	})
	t.Run("disconnected stdout", func(t *testing.T) {
		opens := 0
		cmd := infoCmd(func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(nil, nil, nil, nil), nil
		})
		cmd.SetOut(&infoFailingWriter{max: 1})
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, io.ErrClosedPipe) || opens != 1 {
			t.Fatalf("closed plain report output: err=%v opens=%d", err, opens)
		}
	})
}

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

type diagnosticFailOnWrite struct {
	count, limit int
	failure      error
}

func (w *diagnosticFailOnWrite) Write(p []byte) (int, error) {
	w.count++
	if w.count == w.limit {
		return 0, w.failure
	}
	return len(p), nil
}

func TestSessionCheckDiagnosticsRealSocketAndMissingEnvironment(t *testing.T) {
	runtime, err := os.MkdirTemp("/var/tmp", "pf-sc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	listener, err := net.Listen("unix", filepath.Join(runtime, "wayland-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("WAYLAND_DISPLAY", "wayland-fixture")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/synthetic-bus")
	var output bytes.Buffer
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XDG_RUNTIME_DIR=<set>", "WAYLAND_DISPLAY=wayland-fixture (socket reachable)", "DBUS_SESSION_BUS_ADDRESS=<set>", "/dev/uinput"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("session diagnostics missed %q: %s", want, output.String())
		}
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "nonexistent-wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	output.Reset()
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XDG_RUNTIME_DIR is not set", "socket unresolved", "DBUS_SESSION_BUS_ADDRESS is not set"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing environment not explained: %q in %s", want, output.String())
		}
	}

	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(runtime, "missing"))
	output.Reset()
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "not found") || !strings.Contains(output.String(), "socket missing") {
		t.Fatalf("unreachable paths reported available: %s", output.String())
	}
}
func TestSessionCheckEveryOutputStagePropagatesWriterFailure(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("WAYLAND_DISPLAY", "wayland-missing")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/synthetic")
	var reference bytes.Buffer
	if err := writeSessionCheck(&reference); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("simulated closed diagnostics output")
	for nth := 1; nth <= 9; nth++ {
		w := &diagnosticFailOnWrite{limit: nth, failure: failure}
		err := writeSessionCheck(w)
		if w.count < nth {
			break
		}
		if !errors.Is(err, failure) {
			t.Fatalf("write failure %d swallowed after %d writes: %v", nth, w.count, err)
		}
	}
	for _, tc := range []struct {
		name  string
		write func(*diagnosticFailOnWrite) error
	}{
		{"runtime-dir", func(w *diagnosticFailOnWrite) error { return writeRuntimeDirCheck(w, runtime) }},
		{"missing-runtime-dir", func(w *diagnosticFailOnWrite) error { return writeRuntimeDirCheck(w, "") }},
		{"wayland-unset", func(w *diagnosticFailOnWrite) error { return writeWaylandDisplayCheck(w, "", runtime) }},
		{"wayland-missing", func(w *diagnosticFailOnWrite) error { return writeWaylandDisplayCheck(w, "missing", runtime) }},
		{"DBus-enabled", func(w *diagnosticFailOnWrite) error { return writeDBusCheck(w, "unix:path=/private-bus") }},
		{"DBus-disabled", func(w *diagnosticFailOnWrite) error { return writeDBusCheck(w, "") }},
		{"uinput", func(w *diagnosticFailOnWrite) error { return writeUinputCheck(w) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &diagnosticFailOnWrite{limit: 1, failure: failure}
			if err := tc.write(w); !errors.Is(err, failure) {
				t.Fatalf("diagnostics writer error not propagated: %v", err)
			}
		})
	}
}
func TestSessionCheckRejectsRegularFileMisrepresentedAsWaylandSocket(t *testing.T) {
	runtime := t.TempDir()
	regular := filepath.Join(runtime, "wayland-regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeWaylandCheck(&buf, regular, runtime); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "socket reachable") || !strings.Contains(buf.String(), "socket missing") {
		t.Fatalf("regular file accepted as compositor socket: %s", buf.String())
	}
}

type sessionStartupSignalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *sessionStartupSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "press Ctrl+C") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}
func (w *sessionStartupSignalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestSyntheticSessionStartEmitsEnvironmentAndWaitsForCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := pftest.New(nil, nil, nil, nil)
	opened := 0
	opener := func(got context.Context, options ...perfuncted.Option) (*perfuncted.Session, error) {
		opened++
		if got != ctx || len(options) != 1 {
			t.Fatalf("invalid startup context or options: ctx=%v options=%d", got, len(options))
		}
		return session, nil
	}
	cmd := sessionStartCmdWithOpener(opener)
	cmd.SetContext(ctx)
	var stdout bytes.Buffer
	stderr := &sessionStartupSignalWriter{ready: make(chan struct{})}
	cmd.SetOut(&stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--res-x", "640", "--res-y", "480", "--sway-config", "/tmp/synthetic-config"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	// The session is required to stay alive after reporting readiness; wait
	// for an explicit signal before cancellation rather than racing on a buffer.
	select {
	case <-stderr.ready:
	case err := <-done:
		t.Fatalf("synthetic session exited before readiness: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("session did not report readiness")
	}
	select {
	case err := <-done:
		t.Fatalf("session exited without cancellation: %v", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("startup opened %d sessions", opened)
	}
	for _, value := range []string{"export XDG_RUNTIME_DIR=", "export WAYLAND_DISPLAY=", "export DBUS_SESSION_BUS_ADDRESS="} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("missing startup export %q in %q", value, stdout.String())
		}
	}
	for _, value := range []string{"running", "press Ctrl+C", "stopping", "stopped"} {
		if !strings.Contains(stderr.String(), value) {
			t.Fatalf("missing session lifecycle %q in %q", value, stderr.String())
		}
	}
}

func TestSyntheticSessionStartReportsOpenAndOutputFailures(t *testing.T) {
	wanted := errors.New("synthetic desktop startup failed")
	cmd := sessionStartCmdWithOpener(func(context.Context, ...perfuncted.Option) (*perfuncted.Session, error) { return nil, wanted })
	if err := cmd.Execute(); !errors.Is(err, wanted) {
		t.Fatalf("startup failure lost: %v", err)
	}
	for _, stage := range []struct {
		name string
		out  io.Writer
		err  io.Writer
	}{
		{"stdout 1", &infoFailingWriter{max: 1}, io.Discard},
		{"stdout 2", &infoFailingWriter{max: 2}, io.Discard},
		{"stdout 3", &infoFailingWriter{max: 3}, io.Discard},
		{"stderr 1", io.Discard, &infoFailingWriter{max: 1}},
		{"stderr 2", io.Discard, &infoFailingWriter{max: 2}},
		{"stderr 3", io.Discard, &infoFailingWriter{max: 3}},
	} {
		t.Run(stage.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			cmd := sessionStartCmdWithOpener(func(context.Context, ...perfuncted.Option) (*perfuncted.Session, error) {
				return pftest.New(nil, nil, nil, nil), nil
			})
			cmd.SetContext(ctx)
			cmd.SetOut(stage.out)
			cmd.SetErr(stage.err)
			if err := cmd.Execute(); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("startup swallowed writer failure: %v", err)
			}
		})
	}
}
