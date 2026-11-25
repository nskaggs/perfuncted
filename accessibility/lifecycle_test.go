package accessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDisconnectClassificationPreservesWrappedErrorSemantics(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"identity", ErrDisconnected, true},
		{"wrapped identity", fmt.Errorf("cannot fetch children: %w", ErrDisconnected), true},
		{"lowercase transport", errors.New("connection disconnected by peer"), true},
		{"uppercase transport", errors.New("DBUS DISCONNECTED"), true},
		{"recipient unavailable", errors.New("Message Recipient has disconnected"), true},
		{"no reply", errors.New("No Reply from service"), true},
		{"unrelated permission", errors.New("permission denied"), false},
		{"unrelated timeout", errors.New("context deadline exceeded"), false},
		{"unrelated internal", errors.New("method unavailable"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDisconnectedDBusError(tc.err); got != tc.want {
				t.Fatalf("isDisconnectedDBusError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMatchErrorFormattingUnwrapAndNilCases(t *testing.T) {
	cause := errors.New("secret cause")
	cases := []struct {
		name      string
		value     *MatchError
		want      string
		wantCause error
	}{
		{"nil pointer", nil, "accessibility: match failed", nil},
		{"empty", &MatchError{}, "accessibility: match failed", nil},
		{"operation only", &MatchError{Operation: "find"}, "accessibility: find: match failed", nil},
		{"cause only", &MatchError{Err: cause}, "secret cause", cause},
		{"both", &MatchError{Operation: "find", Err: cause}, "accessibility: find: secret cause", cause},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.value.Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
			if got := tc.value.Unwrap(); got != tc.wantCause { //nolint:errorlint // Unwrap must preserve the exact cause object.
				t.Fatalf("Unwrap() = %v, want %v", got, tc.wantCause)
			}
			if tc.wantCause != nil && !errors.Is(tc.value, tc.wantCause) {
				t.Fatal("wrapped cause no longer discoverable")
			}
			if strings.Contains(tc.value.Error(), "%!") {
				t.Fatal("malformed diagnostic")
			}
		})
	}
}

func TestATSPIRetireEventsForReopenStopsSetupAndDispatcherBeforeReturning(t *testing.T) {
	var setupCancels, dispatcherCancels atomic.Int32
	setupStopped := make(chan struct{})
	dispatcherStopped := make(chan struct{})
	setupDone := make(chan struct{})
	dispatcherDone := make(chan struct{})
	var onceSetup, onceDispatcher sync.Once
	b := &dbusBackend{
		eventStarting:  &eventStart{done: setupDone},
		eventStartStop: func() { setupCancels.Add(1); onceSetup.Do(func() { close(setupStopped) }) },
		eventCancel:    func() { dispatcherCancels.Add(1); onceDispatcher.Do(func() { close(dispatcherStopped) }) },
		eventDone:      dispatcherDone,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- b.RetireEventsForReopen(ctx) }()
	select {
	case <-setupStopped:
	case <-ctx.Done():
		t.Fatalf("setup cancellation not requested: %v", ctx.Err())
	}
	select {
	case <-dispatcherStopped:
	case <-ctx.Done():
		t.Fatalf("dispatcher cancellation not requested: %v", ctx.Err())
	}
	if !b.eventsRetired {
		t.Fatal("backend did not retire event admission before cancelling workers")
	}
	select {
	case err := <-finished:
		t.Fatalf("retirement returned before worker completion: %v", err)
	default:
	}
	close(setupDone)
	select {
	case err := <-finished:
		t.Fatalf("retirement returned with dispatcher active: %v", err)
	default:
	}
	close(dispatcherDone)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("completed retirement failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("retirement did not join workers")
	}
	if setupCancels.Load() != 1 || dispatcherCancels.Load() != 1 {
		t.Fatalf("cancel counts setup=%d dispatcher=%d", setupCancels.Load(), dispatcherCancels.Load())
	}
}
func TestATSPIRetireEventsForReopenRespectsLifetimeCancellationAndPartialSetup(t *testing.T) {
	for _, tc := range []struct {
		name string
		init func(*dbusBackend)
	}{
		{"setup blocked", func(b *dbusBackend) { b.eventStarting = &eventStart{done: make(chan struct{})} }},
		{"dispatcher blocked", func(b *dbusBackend) { b.eventDone = make(chan struct{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{}
			tc.init(b)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := b.RetireEventsForReopen(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled retirement: %v", err)
			}
			if !b.eventsRetired {
				t.Fatal("canceled retirement admitted new subscriptions")
			}
		})
	}
	if err := (*dbusBackend)(nil).RetireEventsForReopen(context.Background()); err != nil {
		t.Fatalf("nil owner retirement: %v", err)
	}
	if err := (&dbusBackend{}).RetireEventsForReopen(absentLifecycleContext); err == nil {
		t.Fatal("nil retirement context accepted")
	}
	b := &dbusBackend{}
	if err := b.RetireEventsForReopen(context.Background()); err != nil || !b.eventsRetired {
		t.Fatalf("idle retirement: state=%v error=%v", b.eventsRetired, err)
	}
}
func TestATSPIReopenRejectsInvalidContextAndUnavailablePrivateTarget(t *testing.T) {
	b := &dbusBackend{generation: 10, runtime: env.FromEnviron([]string{})}
	if _, err := b.Reopen(absentLifecycleContext); err == nil || b.Generation() != 10 {
		t.Fatalf("nil reopen context mutated generation: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Reopen(ctx); !errors.Is(err, context.Canceled) || b.Generation() != 10 {
		t.Fatalf("canceled reopen mutated generation: %v", err)
	}
	if _, err := (*dbusBackend)(nil).Reopen(context.Background()); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend reopened: %v", err)
	}
	if _, err := b.Reopen(context.Background()); !errors.Is(err, ErrDisconnected) || b.Generation() != 11 {
		t.Fatalf("missing address failed to retire prior epoch: err=%v generation=%d", err, b.Generation())
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentLifecycleContext context.Context

type failingAccessibilityBusProvider struct{}

func (failingAccessibilityBusProvider) GetAddress() (string, *dbus.Error) {
	return "", dbus.MakeFailedError(errors.New("synthetic address lookup denied"))
}

func TestOpenRuntimeReportsAccessibilityBusProviderFailure(t *testing.T) {
	sessionAddress := startTestDBus(t)
	session, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private session bus: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Export(failingAccessibilityBusProvider{}, busPath, busService); err != nil {
		t.Fatalf("export failing accessibility bus provider: %v", err)
	}
	reply, err := session.RequestName(busService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus service: reply=%v err=%v", reply, err)
	}

	backend, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress,
	}))
	if backend != nil {
		_ = backend.Close()
		t.Fatalf("OpenRuntimeContext returned backend %T after address lookup failed", backend)
	}
	if err == nil || !strings.Contains(err.Error(), "get accessibility bus address") || !strings.Contains(err.Error(), "synthetic address lookup denied") {
		t.Fatalf("OpenRuntimeContext error = %v, want wrapped provider error", err)
	}
}

func TestATSPIReopenCreatesFreshBackendGenerationOnTargetBuses(t *testing.T) {
	sessionAddress := startTestDBus(t)
	accessibilityAddress := startTestDBus(t)
	sessionConn, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private session bus: %v", err)
	}
	t.Cleanup(func() { _ = sessionConn.Close() })
	if err := sessionConn.Export(&testAccessibilityBusProvider{address: accessibilityAddress}, busPath, busService); err != nil {
		t.Fatalf("export accessibility bus provider: %v", err)
	}
	if reply, err := sessionConn.RequestName(busService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus provider: reply=%d err=%v", reply, err)
	}

	runtime := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})
	opened, err := OpenRuntimeContext(context.Background(), runtime)
	if err != nil {
		t.Fatalf("open initial accessibility backend: %v", err)
	}
	initial, ok := opened.(*dbusBackend)
	if !ok {
		t.Fatalf("initial backend type=%T, want *dbusBackend", opened)
	}
	t.Cleanup(func() { _ = initial.Close() })

	reopenedBackend, err := initial.Reopen(context.Background())
	if err != nil {
		t.Fatalf("reopen accessibility backend: %v", err)
	}
	reopened, ok := reopenedBackend.(*dbusBackend)
	if !ok {
		t.Fatalf("reopened backend type=%T, want *dbusBackend", reopenedBackend)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened == initial || reopened.access == initial.access || reopened.session == initial.session {
		t.Fatal("reopen reused a prior backend transport")
	}
	if got, want := reopened.Generation(), initial.Generation()+1; got != want {
		t.Fatalf("reopened generation=%d, want %d", got, want)
	}
	if err := reopened.connected(); err != nil {
		t.Fatalf("fresh backend is disconnected: %v", err)
	}
	if err := initial.connected(); err != nil {
		t.Fatalf("reopen revoked the prior backend before its owner closed it: %v", err)
	}
}

func TestATSPIReopenConnectionFailureInvalidatesPriorBackend(t *testing.T) {
	missingBus := filepath.Join(t.TempDir(), "missing-bus")
	backend := &dbusBackend{
		runtime:    env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=" + missingBus}),
		generation: 12,
	}
	if _, err := backend.Reopen(context.Background()); err == nil {
		t.Fatal("reopen accepted an unavailable target session bus")
	}
	if !errors.Is(backend.connected(), ErrDisconnected) {
		t.Fatalf("backend after failed transport reopen = %v, want disconnected", backend.connected())
	}
	if got := backend.Generation(); got != 13 {
		t.Fatalf("generation after failed transport reopen=%d, want 13", got)
	}
}

func TestOpenRuntimeRejectsInvalidTargetAccessibilityRoutes(t *testing.T) {
	sessionAddress := startTestDBus(t)
	session, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private target session bus: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	provider := &testAccessibilityBusProvider{}
	if err := session.Export(provider, busPath, busService); err != nil {
		t.Fatalf("export private accessibility bus provider: %v", err)
	}
	if reply, err := session.RequestName(busService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus service: reply=%v err=%v", reply, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		rt   env.Runtime
		want error
	}{
		{name: "nil context", rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})},
		{name: "canceled context", ctx: canceled, rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress}), want: context.Canceled},
		{name: "missing target session bus", ctx: context.Background(), rt: env.FromEnviron([]string{})},
		{name: "malformed target session bus", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address"})},
		{name: "malformed accessibility override", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress, "ATSPI_BUS_ADDRESS=not-a-dbus-address"})},
		{name: "empty accessibility address", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, err := OpenRuntimeContext(tc.ctx, tc.rt)
			if backend != nil {
				_ = backend.Close()
				t.Fatalf("invalid target route returned backend %T", backend)
			}
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("OpenRuntimeContext error = %v, want an error matching %v", err, tc.want)
			}
		})
	}
}

func TestToolkitIdentityCacheFollowsCurrentBackendGeneration(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	if _, err := backend.toolkitForNode(nil, id); err == nil {
		t.Fatal("nil toolkit lookup context was accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := backend.toolkitForNode(canceled, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled toolkit lookup = %v", err)
	}

	toolkit, err := backend.toolkitForNode(ctx, id)
	if err != nil || toolkit != "GTK" {
		t.Fatalf("toolkit lookup = %q, %v", toolkit, err)
	}
	if toolkit, err := backend.toolkitForNode(ctx, id); err != nil || toolkit != "GTK" {
		t.Fatalf("cached toolkit lookup = %q, %v", toolkit, err)
	}
	backend.mu.RLock()
	_, cached := backend.toolkits[id.BusName]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("successful toolkit lookup was not cached")
	}

	backend.Invalidate(id)
	if _, err := backend.toolkitForNode(ctx, id); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit handle error = %v", err)
	}
	current := backend.refID(objectRef{BusName: registryName, ObjectPath: dbus.ObjectPath(id.ObjectPath)})
	toolkit, err = backend.toolkitForNode(ctx, current)
	if err != nil || toolkit != "GTK" || current.Generation <= id.Generation {
		t.Fatalf("toolkit after generation change = %q, handle=%+v err=%v", toolkit, current, err)
	}
}
