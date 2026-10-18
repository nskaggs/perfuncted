package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/screen"
)

type startupEvent struct {
	phase StartupPhase
	state StartupPhaseState
}

func TestOpenStartupObserverReportsHealthyHeadlessInitialization(t *testing.T) {
	prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}

	var events []startupEvent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := Open(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond}}),
		Require(CapabilityScreen),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			events = append(events, startupEvent{phase: phase, state: state})
		}),
	)
	if err != nil {
		t.Fatalf("synthetic headless Open: %v", err)
	}
	if session.Target().Kind() != TargetHeadless {
		t.Fatalf("Open target = %q, want headless", session.Target().Kind())
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}

	for _, want := range []startupEvent{
		{StartupPhaseStaleSessionCleanup, StartupPhaseStarted},
		{StartupPhaseStaleSessionCleanup, StartupPhaseSucceeded},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseStarted},
		{StartupPhaseRuntimeDirectoryPreparation, StartupPhaseSucceeded},
		{StartupPhaseDBusStartup, StartupPhaseSucceeded},
		{StartupPhaseDBusProcessSpawn, StartupPhaseSucceeded},
		{StartupPhaseDBusSocketReady, StartupPhaseSucceeded},
		{StartupPhaseSwayConfiguration, StartupPhaseSucceeded},
		{StartupPhaseSwayStartup, StartupPhaseSucceeded},
		{StartupPhaseSwayProcessSpawn, StartupPhaseSucceeded},
		{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded},
		{StartupPhaseClipboardHelper, StartupPhaseSucceeded},
		{StartupPhaseCapabilityScreen, StartupPhaseSucceeded},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseSucceeded},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("healthy startup omitted %+v from events %+v", want, events)
		}
	}
	for _, event := range events {
		if event.state == StartupPhaseFailed || event.state == StartupPhaseCanceled || event.state == StartupPhaseTimedOut {
			t.Fatalf("healthy startup reported terminal failure: %+v", event)
		}
		if strings.Contains(string(event.phase), string(filepath.Separator)) {
			t.Fatalf("startup phase contains a path: %+v", event)
		}
	}
}

func TestOpenStartupObserverIdentifiesCanceledHeadlessDBusReadiness(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := make(chan startupEvent, 32)
	result := make(chan error, 1)
	go func() {
		_, err := Open(
			ctx,
			WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 30 * time.Second, Poll: time.Millisecond}}),
			WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
				events <- startupEvent{phase: phase, state: state}
			}),
		)
		result <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event == (startupEvent{StartupPhaseDBusSocketReady, StartupPhaseStarted}) {
				cancel()
				goto canceled
			}
		case <-deadline.C:
			cancel()
			t.Fatal("headless startup did not reach the synthetic D-Bus readiness wait")
		}
	}

canceled:
	var openErr error
	select {
	case openErr = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("Open did not return after canceling synthetic D-Bus readiness")
	}
	if !errors.Is(openErr, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", openErr)
	}
	for {
		select {
		case event := <-events:
			if event == (startupEvent{StartupPhaseDBusSocketReady, StartupPhaseCanceled}) {
				goto checked
			}
		default:
			t.Fatal("D-Bus readiness cancellation result was not reported")
		}
	}

checked:
	for _, phase := range []StartupPhase{
		StartupPhaseSwayProcessSpawn,
		StartupPhaseSwaySocketsReady,
		StartupPhaseCapabilityScreen,
	} {
		if hasStartupPhase(events, phase) {
			t.Fatalf("startup passed canceled D-Bus readiness and entered %q", phase)
		}
	}
	requireNoManagedSessionDirs(t, tmp)
}

func TestOpenStartupObserverIdentifiesTimedOutHeadlessDBusReadiness(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	events := make(chan startupEvent, 32)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := Open(
			ctx,
			WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: time.Second, Poll: time.Millisecond}}),
			WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
				events <- startupEvent{phase: phase, state: state}
				if phase == StartupPhaseDBusSocketReady && state == StartupPhaseStarted {
					close(started)
				}
			}),
		)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("headless startup did not reach the synthetic D-Bus readiness wait")
	}
	var openErr error
	select {
	case openErr = <-result:
	case <-time.After(time.Second):
		t.Fatal("Open did not return after the synthetic startup deadline")
	}
	if !errors.Is(openErr, context.DeadlineExceeded) {
		t.Fatalf("Open error = %v, want context.DeadlineExceeded", openErr)
	}
	observed := drainStartupEvents(events)
	for _, want := range []startupEvent{
		{StartupPhaseDBusSocketReady, StartupPhaseTimedOut},
		{StartupPhaseDBusStartup, StartupPhaseTimedOut},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseTimedOut},
	} {
		if !hasStartupEvent(observed, want) {
			t.Fatalf("timed-out startup omitted %+v from events %+v", want, observed)
		}
	}
	if hasStartupEvent(observed, startupEvent{StartupPhaseSwayProcessSpawn, StartupPhaseStarted}) {
		t.Fatal("startup passed the timed-out D-Bus readiness stage")
	}
	requireNoManagedSessionDirs(t, tmp)
}

func TestOpenStartupObserverCallbackPanicDoesNotBreakOpen(t *testing.T) {
	prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := Open(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond}}),
		Require(CapabilityScreen),
		WithStartupPhaseObserver(func(StartupPhase, StartupPhaseState) {
			panic("synthetic observer panic")
		}),
	)
	if err != nil {
		t.Fatalf("observer panic changed Open result: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}
}

func TestOpenStartupObserverReportsOptionalAccessibilityAddressTimeout(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	savedAccessibility := openAccessibility
	t.Cleanup(func() { openAccessibility = savedAccessibility })
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}
	openAccessibility = func(context.Context, env.Runtime) (accessibility.Backend, error) {
		return &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}, nil
	}
	launcher := filepath.Join(tmp, "bin", atspiBusLauncherName)
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write synthetic AT-SPI launcher: %v", err)
	}
	var events []startupEvent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := Open(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{
			Startup: time.Second,
			Medium:  30 * time.Millisecond,
			Poll:    time.Millisecond,
		}}),
		Require(CapabilityScreen),
		Optional(CapabilityAccessibility),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			events = append(events, startupEvent{phase: phase, state: state})
		}),
	)
	if err != nil {
		t.Fatalf("Open with optional synthetic accessibility: %v", err)
	}
	if !session.Has(CapabilityScreen) || !session.Has(CapabilityAccessibility) {
		t.Fatalf("optional accessibility address timeout changed capability availability: screen=%v accessibility=%v", session.Has(CapabilityScreen), session.Has(CapabilityAccessibility))
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}
	for _, want := range []startupEvent{
		{StartupPhaseAccessibilityLauncher, StartupPhaseSucceeded},
		{StartupPhaseAccessibilityBusAddress, StartupPhaseStarted},
		{StartupPhaseAccessibilityBusAddress, StartupPhaseTimedOut},
		{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded},
		{StartupPhaseCapabilityAccessibility, StartupPhaseSucceeded},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("optional accessibility replay omitted %+v from events %+v", want, events)
		}
	}
}

func prepareHeadlessHelpers(t *testing.T, ready bool) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", "pf-open-")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.MkdirTemp("/tmp", "pfo-")
	if err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, alias); err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(alias)
		_ = os.RemoveAll(tmp)
	})
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dbusBody := "exec /bin/sleep 30"
	swayBody := "exec /bin/sleep 30"
	if ready {
		dbusBody = ": > \"$XDG_RUNTIME_DIR/bus\"\n" + dbusBody
		swayBody = ": > \"$XDG_RUNTIME_DIR/wayland-1\"\n: > \"$XDG_RUNTIME_DIR/sway-ipc.synthetic.sock\"\n" + swayBody
	}
	for name, body := range map[string]string{
		"dbus-daemon": dbusBody,
		"sway":        swayBody,
		"wl-paste":    "exec /bin/sleep 30",
	} {
		path := filepath.Join(bin, name)
		contents := fmt.Sprintf("#!/bin/sh\nset -eu\n%s\n", body)
		if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
			t.Fatalf("write synthetic %s helper: %v", name, err)
		}
	}
	return tmp
}

func hasStartupEvent(events []startupEvent, want startupEvent) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func hasStartupPhase(events <-chan startupEvent, phase StartupPhase) bool {
	for {
		select {
		case event := <-events:
			if event.phase == phase {
				return true
			}
		default:
			return false
		}
	}
}

func drainStartupEvents(events <-chan startupEvent) []startupEvent {
	var drained []startupEvent
	for {
		select {
		case event := <-events:
			drained = append(drained, event)
		default:
			return drained
		}
	}
}

// requireNoManagedSessionDirs verifies failed managed startup never leaks its
// runtime directory, regardless of which stage failed.
func requireNoManagedSessionDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read synthetic temp root: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed startup left managed runtime directory %q", entry.Name())
		}
	}
}
