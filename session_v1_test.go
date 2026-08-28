package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"image"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

type capabilityScreen struct {
	closeCalls atomic.Int32
}

type operationReportingScreen struct {
	capabilityScreen
}

func (*operationReportingScreen) SupportedOperations() []string {
	return []string{"hash"}
}

func (s *capabilityScreen) Grab(
	context.Context,
	image.Rectangle,
) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (s *capabilityScreen) GrabFullHash(context.Context) (uint32, error) {
	return 1, nil
}

func (s *capabilityScreen) GrabRegionHash(
	context.Context,
	image.Rectangle,
) (uint32, error) {
	return 1, nil
}

func (s *capabilityScreen) Close() error {
	s.closeCalls.Add(1)
	return nil
}

type capabilityClipboard struct{}

type cancelDuringOpenContext struct {
	context.Context //nolint:containedctx // deterministic test context
	done            chan struct{}
	cancelOnce      func()
	checks          atomic.Int32
}

func (c *cancelDuringOpenContext) Err() error {
	if c.checks.Add(1) > 1 {
		c.cancelOnce()
		return context.Canceled
	}
	return nil
}

func (c *cancelDuringOpenContext) Done() <-chan struct{} { return c.done }

func (*capabilityClipboard) Get(context.Context) (string, error) {
	return "", nil
}

func (*capabilityClipboard) Set(context.Context, string) error {
	return nil
}

func (*capabilityClipboard) Close() error {
	return nil
}

func preserveOpeners(t *testing.T) {
	t.Helper()
	savedScreen := openScreen
	savedInput := openInput
	savedWindow := openWindow
	savedOutput := openOutput
	savedClipboard := openClipboard
	t.Cleanup(func() {
		openScreen = savedScreen
		openInput = savedInput
		openWindow = savedWindow
		openOutput = savedOutput
		openClipboard = savedClipboard
	})
}

func TestOpenLeavesUnrequestedCapabilitiesClosed(t *testing.T) {
	preserveOpeners(t)
	var calls atomic.Int32
	openScreen = func(env.Runtime) (screen.Screenshotter, error) {
		calls.Add(1)
		return nil, errors.New("unexpected screen open")
	}
	openInput = func(env.Runtime, int32, int32) (input.Inputter, error) {
		calls.Add(1)
		return nil, errors.New("unexpected input open")
	}
	openWindow = func(env.Runtime) (window.Manager, error) {
		calls.Add(1)
		return nil, errors.New("unexpected window open")
	}
	openOutput = func(env.Runtime) (output.Lister, error) {
		calls.Add(1)
		return nil, errors.New("unexpected output open")
	}
	openClipboard = func(env.Runtime) (clipboard.Clipboard, error) {
		calls.Add(1)
		return nil, errors.New("unexpected clipboard open")
	}

	session, err := Open(context.Background(), WithLogger(nil))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	if calls.Load() != 0 {
		t.Fatalf("backend open calls = %d, want 0", calls.Load())
	}
	for _, status := range session.Capabilities() {
		if status.Requested || status.Available || status.Failure != nil {
			t.Fatalf("unrequested status = %+v", status)
		}
	}
	if session.Screen == nil ||
		session.Input == nil ||
		session.Windows == nil ||
		session.Outputs == nil ||
		session.Clipboard == nil {
		t.Fatal("Open exposed a nil capability facade")
	}
}

func TestOpenRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	}
}

func TestOpenRejectsContextCanceledDuringInitialization(t *testing.T) {
	done := make(chan struct{})
	ctx := &cancelDuringOpenContext{
		Context: context.Background(),
		done:    done,
		cancelOnce: sync.OnceFunc(func() {
			close(done)
		}),
	}

	if session, err := Open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	} else if session != nil {
		t.Fatal("Open returned a session after cancellation")
	}
}

func TestOpenRequiredAndOptionalCapabilities(t *testing.T) {
	preserveOpeners(t)
	clipboardBackend := &capabilityClipboard{}
	optionalErr := errors.New("outputs unavailable")
	openClipboard = func(env.Runtime) (clipboard.Clipboard, error) {
		return clipboardBackend, nil
	}
	openOutput = func(env.Runtime) (output.Lister, error) {
		return nil, optionalErr
	}

	session, err := Open(
		context.Background(),
		Require(CapabilityClipboard),
		Optional(CapabilityOutputs),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	if !session.Has(CapabilityClipboard) {
		t.Fatal("required clipboard is unavailable")
	}
	clipboardStatus := session.Capability(CapabilityClipboard)
	if !clipboardStatus.Required ||
		!clipboardStatus.Available ||
		clipboardStatus.Backend == "" ||
		!clipboardStatus.Supports("set") {
		t.Fatalf("clipboard status = %+v", clipboardStatus)
	}
	outputStatus := session.Capability(CapabilityOutputs)
	if outputStatus.Available ||
		outputStatus.Required ||
		!errors.Is(outputStatus.Failure, optionalErr) {
		t.Fatalf("output status = %+v", outputStatus)
	}
	_, err = session.Outputs.List(context.Background())
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityOutputs ||
		!errors.Is(err, optionalErr) {
		t.Fatalf("Outputs.List error = %v", err)
	}
}

func TestOpenRequiredFailureCleansPartialCapabilities(t *testing.T) {
	preserveOpeners(t)
	screenBackend := &capabilityScreen{}
	inputErr := errors.New("input unavailable")
	openScreen = func(env.Runtime) (screen.Screenshotter, error) {
		return screenBackend, nil
	}
	openInput = func(env.Runtime, int32, int32) (input.Inputter, error) {
		return nil, inputErr
	}

	session, err := Open(
		context.Background(),
		Require(CapabilityScreen, CapabilityInput),
	)
	if session != nil {
		t.Fatal("Open returned a Session after required failure")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityInput ||
		!errors.Is(err, inputErr) {
		t.Fatalf("Open error = %v", err)
	}
	if screenBackend.closeCalls.Load() != 1 {
		t.Fatalf(
			"screen close calls = %d, want 1",
			screenBackend.closeCalls.Load(),
		)
	}
}

func TestOpenTreatsNilBackendAsUnavailable(t *testing.T) {
	preserveOpeners(t)
	openScreen = func(env.Runtime) (screen.Screenshotter, error) {
		return nil, nil
	}

	session, err := Open(context.Background(), Require(CapabilityScreen))
	if session != nil {
		t.Fatal("Open returned a session for a nil backend")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityScreen ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open error = %v, want screen unavailable capability error", err)
	}
}

func TestOpenTreatsTypedNilBackendAsUnavailable(t *testing.T) {
	preserveOpeners(t)
	var backend *capabilityScreen
	openScreen = func(env.Runtime) (screen.Screenshotter, error) {
		return backend, nil
	}

	session, err := Open(context.Background(), Require(CapabilityScreen))
	if session != nil {
		t.Fatal("Open returned a session for a typed nil backend")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityScreen ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open error = %v, want screen unavailable capability error", err)
	}
}

func TestNewSessionForTestingTreatsTypedNilBackendAsUnavailable(t *testing.T) {
	var backend *capabilityScreen
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	if session.Has(CapabilityScreen) {
		t.Fatal("typed-nil screen backend was reported as available")
	}
	_, err := session.Screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("typed-nil screen operation error = %v, want ErrUnavailable", err)
	}
}

func TestSessionEnforcesBackendOperationReport(t *testing.T) {
	session := NewSessionForTesting(
		&operationReportingScreen{},
		nil,
		nil,
		nil,
		nil,
	)
	t.Cleanup(func() { _ = session.Close() })

	status := session.Capability(CapabilityScreen)
	if !status.Available || status.Supports("capture") {
		t.Fatalf("screen status = %+v, want available without capture", status)
	}
	_, err := session.Screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unadvertised capture error = %v, want ErrUnsupported", err)
	}
}

func TestTargetOptionsAreMutuallyExclusive(t *testing.T) {
	_, err := Open(
		context.Background(),
		WithTarget(EnvironmentTarget([]string{})),
		WithHeadless(SessionConfig{}),
	)
	if err == nil {
		t.Fatal("Open accepted multiple target options")
	}
}

func TestLaunchRoutesEnvironmentAndIgnoresLaterContextCancellation(
	t *testing.T,
) {
	session, err := Open(
		context.Background(),
		WithTarget(EnvironmentTarget([]string{
			"WAYLAND_DISPLAY=target-wayland",
			"DISPLAY=",
		})),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})

	var stdout bytes.Buffer
	launchCtx, cancel := context.WithCancel(context.Background())
	app, err := session.Launch(
		launchCtx,
		Command{
			Name: "sh",
			Args: []string{
				"-c",
				`printf '%s|%s' "$CUSTOM" "$WAYLAND_DISPLAY"; sleep 0.02`,
			},
			Env: []string{
				"CUSTOM=caller",
				"WAYLAND_DISPLAY=wrong",
			},
			Stdout: &stdout,
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if app.Exited() {
		t.Fatal("application reported exited immediately after launch")
	}
	cancel()
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after launch cancellation: %v", err)
	}
	if !app.Exited() {
		t.Fatal("application did not report exited after Wait")
	}
	if got := stdout.String(); got != "caller|target-wayland" {
		t.Fatalf("stdout = %q", got)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("repeated Wait: %v", err)
	}
}

func TestSessionCloseStopsApplicationsInReverseLaunchOrder(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.config.ApplicationGracePeriod = time.Second
	orderPath := t.TempDir() + "/order"
	launch := func(name string) {
		t.Helper()
		script := "trap 'printf " + name + " >> \"$ORDER\"; exit 0' TERM; " +
			"while :; do sleep 1; done"
		if _, err := session.Launch(
			context.Background(),
			Command{
				Name: "sh",
				Args: []string{"-c", script},
				Env:  []string{"ORDER=" + orderPath},
			},
		); err != nil {
			t.Fatalf("Launch %s: %v", name, err)
		}
	}
	launch("a")
	launch("b")
	time.Sleep(30 * time.Millisecond)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "ba" {
		t.Fatalf("shutdown order = %q, want ba", got)
	}
	if _, err := session.Launch(
		context.Background(),
		Command{Name: "true"},
	); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Launch after Close error = %v", err)
	}
}

func TestApplicationStopRejectsNilContext(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	app, err := session.Launch(
		context.Background(),
		Command{Name: "sleep", Args: []string{"10"}},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	var nilCtx context.Context
	if err := app.Stop(nilCtx); err == nil {
		t.Fatal("Stop(nil) returned nil")
	} else if !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("Stop(nil) error = %v, want nil-context error", err)
	}

	if app.Exited() {
		t.Fatal("Stop(nil) terminated the application")
	}
	if err := app.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := app.Wait(context.Background()); err == nil {
		t.Fatal("Wait after Kill returned nil")
	}
}

func TestStartupWaitHelpersHonorContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, test := range []struct {
		name string
		wait func(context.Context) error
	}{
		{
			name: "file",
			wait: func(ctx context.Context) error {
				return waitForFile(ctx, t.TempDir()+"/missing", 100, time.Second)
			},
		},
		{
			name: "glob",
			wait: func(ctx context.Context) error {
				return waitForGlob(ctx, t.TempDir()+"/*", 100, time.Second)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.wait(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("wait error = %v, want context.Canceled", err)
			}
		})
	}
}
