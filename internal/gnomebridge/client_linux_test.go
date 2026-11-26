//go:build linux

package gnomebridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"image"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type malformedBridgeReply struct{}

func (malformedBridgeReply) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	return &dbus.Call{Body: []any{"not the numeric response field"}}
}

// Cancellation causes must remain distinguishable from a generic canceled
// operation; a closed client or transport without descriptor passing must
// never dispatch input or capture operations to a stale GNOME bridge object.
func TestBridgeCancellationAndDisconnectedTransportFailClosed(t *testing.T) {
	cause := errors.New("caller revoked bridge authority")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := bridgeContextError(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("bridge cancellation dropped its cause: %v", err)
	}
	if err := bridgeContextError(context.Background()); err != nil {
		t.Fatalf("live bridge context rejected: %v", err)
	}
	closed := &Client{closed: true, obj: malformedBridgeReply{}}
	if err := closed.Key(context.Background(), 37, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed client accepted synthetic key injection: %v", err)
	}
	if _, err := closed.CaptureFull(context.Background(), 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed client accepted screenshot request: %v", err)
	}
	disconnected := &Client{obj: malformedBridgeReply{}}
	if _, err := disconnected.CaptureFull(context.Background(), 0); !errors.Is(err, ErrUnixFDUnsupported) {
		t.Fatalf("transport lacking file descriptor passing accepted capture: %v", err)
	}
	if _, err := disconnected.CaptureRegion(context.Background(), 0, image.Rect(0, 0, 0, 1)); err == nil || !strings.Contains(err.Error(), "empty capture region") {
		t.Fatalf("empty capture region unexpectedly accepted: %v", err)
	}
	malformed := &Client{obj: malformedBridgeReply{}, supportsFDs: true}
	var number uint32
	if err := malformed.callWithArgsOut(context.Background(), InputInterface, "PointerLocation", nil, &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed input D-Bus reply authorized data: %v", err)
	}
	if err := malformed.capture(context.Background(), "CaptureFull", 0, nil, &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed screenshot D-Bus reply authorized data: %v", err)
	}
}

// Both methods with typed replies and screenshot capture must reject malformed
// transport data. Capture must also retain the caller's cancellation cause
// when a D-Bus call is interrupted after it has reached the provider.
func TestBridgeCaptureCancellationCauseAndTypedReplyRejection(t *testing.T) {
	malformed := &Client{obj: malformedBridgeReply{}}
	var number uint32
	if err := malformed.call(context.Background(), CoreInterface, "GetProtocolVersion", &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("invalid typed GNOME bridge reply authorized data: %v", err)
	}
	object := &blockingObject{seen: make(chan context.Context, 1)}
	client := &Client{obj: object, supportsFDs: true}
	cause := errors.New("screenshot authorization was revoked")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.capture(ctx, "CaptureFull", 0, nil) }()
	select {
	case <-object.seen:
	case <-time.After(time.Second):
		cancel(cause)
		t.Fatal("capture never entered bounded D-Bus call")
	}
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("canceled capture lost operation or revocation cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not terminate when revoked")
	}
	preCanceled, preCancel := context.WithCancel(context.Background())
	preCancel()
	if err := client.capture(preCanceled, "CaptureFull", 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled capture bypassed cancellation: %v", err)
	}
}

type mismatchedWindowReadObject struct{ returnedID string }

func (o mismatchedWindowReadObject) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	return &dbus.Call{Body: []any{WindowInfo{ID: o.returnedID, Title: "another private document"}}}
}

// A late or misrouted D-Bus reply must never expose metadata belonging to a
// different window. The requested ID is the caller's only authorization scope.
func TestBridgeGetWindowRejectsMismatchedOrMissingReplyIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		valid    bool
	}{
		{"different window", "18", false},
		{"missing id", "", false},
		{"whitespace id", "   ", false},
		{"matching identity", "17", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{obj: mismatchedWindowReadObject{returnedID: tc.id}, closeDone: make(chan struct{})}
			window, err := c.GetWindow(context.Background(), "17")
			if tc.valid {
				if err != nil || window.ID != "17" {
					t.Fatalf("correct identity rejected: %+v %v", window, err)
				}
			} else if !errors.Is(err, ErrObjectNotFound) || window.ID != "" || window.Title != "" {
				t.Fatalf("foreign window metadata leaked: %+v %v", window, err)
			}
		})
	}
}

func TestBridgeGetWindowMissingRequestIdentityNeverReachesProvider(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion}
	c := &Client{obj: object, closeDone: make(chan struct{})}
	for _, id := range []string{"", " ", "\t"} {
		info, err := c.GetWindow(context.Background(), id)
		if !errors.Is(err, ErrObjectNotFound) || info != (WindowInfo{}) {
			t.Fatalf("missing request ID %q returned %+v %v", id, info, err)
		}
	}
	if len(object.calls) != 0 {
		t.Fatalf("invalid requests reached GNOME bridge: %+v", object.calls)
	}
}

// D-Bus can acknowledge input or screenshot operations after their caller has
// revoked authority. Both transports must return cancellation, preserving a
// distinct cause when present, and must not report a successful side effect.
func TestGNOMEBridgeInFlightInputAndCaptureCancellation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		capture       bool
		distinctCause bool
	}{
		{name: "input cancellation with reason", distinctCause: true},
		{name: "input plain cancellation"},
		{name: "capture plain cancellation", capture: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := &blockingObject{seen: make(chan context.Context, 1)}
			client := &Client{obj: object, supportsFDs: true}
			ctx, cancel := context.WithCancelCause(context.Background())
			result := make(chan error, 1)
			go func() {
				if tc.capture {
					result <- client.capture(ctx, "CaptureFull", 0, nil)
					return
				}
				result <- client.Key(ctx, 65, true)
			}()
			select {
			case <-object.seen:
			case <-time.After(time.Second):
				cancel(nil)
				t.Fatal("in-flight operation never reached its bounded bridge call")
			}
			reason := errors.New("input session authority withdrawn")
			if tc.distinctCause {
				cancel(reason)
			} else {
				cancel(nil)
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("revoked operation reported success or wrong failure: %v", err)
				}
				if tc.distinctCause && !errors.Is(err, reason) {
					t.Fatalf("revocation cause was lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("bridge operation did not unblock when canceled")
			}
		})
	}
}

// A bridge event must carry a concrete window identity before entering the
// subscription queue. Empty identifiers are not valid lifecycle transitions.
func TestBridgeRefusesWindowLifecycleSignalsWithoutConcreteIdentity(t *testing.T) {
	for _, kind := range []string{"WindowRemoved", "FocusChanged", "WindowAdded", "WindowChanged"} {
		for _, id := range []string{"", "   ", "\t"} {
			t.Run(kind+"/"+id, func(t *testing.T) {
				body := []any{id}
				if kind == "WindowAdded" || kind == "WindowChanged" {
					body = []any{WindowInfo{ID: id, Title: "Private browser window"}}
				}
				signal := &dbus.Signal{Name: WindowsInterface + "." + kind, Path: dbus.ObjectPath(ObjectPath), Body: body}
				if event, ok := decodeWindowEvent(signal); ok {
					t.Fatalf("unidentifiable event admitted: %+v", event)
				}
			})
		}
	}
}

// A D-Bus service can acknowledge an operation at the exact moment its caller
// cancels. A late success must not be exposed as a completed caller operation.
type lateSuccessfulBridgeReply struct{ cancel context.CancelFunc }

func (o lateSuccessfulBridgeReply) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	o.cancel()
	switch method {
	case WindowsInterface + ".GetWindow":
		return &dbus.Call{Body: []any{WindowInfo{ID: "7", Title: "synthetic editor"}}}
	case ClipboardInterface + ".GetText":
		return &dbus.Call{Body: []any{"private synthetic clipboard"}}
	case ScreenInterface + ".CaptureFull":
		return &dbus.Call{Body: []any{int32(0), int32(0), int32(640), int32(480), int32(640), int32(480), float64(1)}}
	default:
		return &dbus.Call{}
	}
}
func TestBridgeLateProviderSuccessDoesNotMaskCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{"core ping", func(ctx context.Context, c *Client) error { return c.Ping(ctx) }},
		{"window mutation", func(ctx context.Context, c *Client) error { return c.Move(ctx, "7", 20, 30) }},
		{"typed window read", func(ctx context.Context, c *Client) error { _, err := c.GetWindow(ctx, "7"); return err }},
		{"clipboard read", func(ctx context.Context, c *Client) error { _, err := c.GetText(ctx); return err }},
		{"image UnixFD transfer", func(ctx context.Context, c *Client) error { _, err := c.CaptureFull(ctx, 7); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &Client{obj: lateSuccessfulBridgeReply{cancel: cancel}, supportsFDs: true, closeDone: make(chan struct{})}
			if err := tc.call(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("late provider success acknowledged on canceled %s: %v", tc.name, err)
			}
		})
	}
}

// The extension metadata handshake is atomic. A malformed or failing
// intermediate reply must never publish partially negotiated capabilities.
type negotiationFaultObject struct {
	failureMethod   string
	malformedMethod string
	seen            []string
}

func (o *negotiationFaultObject) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	o.seen = append(o.seen, method)
	if strings.HasSuffix(method, o.failureMethod) && o.failureMethod != "" {
		return &dbus.Call{Err: errors.New("synthetic negotiation failure")}
	}
	if strings.HasSuffix(method, o.malformedMethod) && o.malformedMethod != "" {
		return &dbus.Call{Body: []any{uint64(37)}}
	}
	switch method {
	case CoreInterface + ".GetProtocolVersion":
		return &dbus.Call{Body: []any{ProtocolVersion}}
	case CoreInterface + ".GetExtensionVersion":
		return &dbus.Call{Body: []any{"synthetic-extension"}}
	case CoreInterface + ".GetShellVersion":
		return &dbus.Call{Body: []any{"synthetic-shell"}}
	case CoreInterface + ".GetCapabilities":
		return &dbus.Call{Body: []any{[]string{"windows", "screen"}}}
	}
	return &dbus.Call{}
}
func TestBridgeNegotiationFailuresCannotPublishPartialMetadata(t *testing.T) {
	for _, stage := range []string{"GetProtocolVersion", "GetExtensionVersion", "GetShellVersion", "GetCapabilities"} {
		for _, fault := range []string{"error", "malformed"} {
			t.Run(stage+"/"+fault, func(t *testing.T) {
				assertNegotiationFaultDoesNotPublishMetadata(t, stage, fault)
			})
		}
	}
	var c *Client
	if c.ProtocolVersion() != 0 || c.ExtensionVersion() != "" || c.ShellVersion() != "" || c.Capabilities() != nil || c.HasCapability("windows") {
		t.Fatal("nil client exposed capability authority")
	}
}

func assertNegotiationFaultDoesNotPublishMetadata(t *testing.T, stage, fault string) {
	t.Helper()
	o := &negotiationFaultObject{}
	if fault == "error" {
		o.failureMethod = stage
	} else {
		o.malformedMethod = stage
	}
	c := &Client{obj: o, closeDone: make(chan struct{})}
	err := negotiate(context.Background(), c)
	if err == nil {
		t.Fatalf("%s %s response was accepted", stage, fault)
	}
	if c.ProtocolVersion() != 0 || c.ExtensionVersion() != "" || c.ShellVersion() != "" || len(c.Capabilities()) != 0 {
		t.Fatalf("partial negotiation exposed: version=%d ext=%s shell=%s caps=%v", c.ProtocolVersion(), c.ExtensionVersion(), c.ShellVersion(), c.Capabilities())
	}
	if !strings.HasSuffix(o.seen[len(o.seen)-1], stage) || len(o.seen) != map[string]int{"GetProtocolVersion": 1, "GetExtensionVersion": 2, "GetShellVersion": 3, "GetCapabilities": 4}[stage] {
		t.Fatalf("failure did not stop at the requested stage: %v", o.seen)
	}
}

func TestBridgeMalformedWindowSignalsCannotEnterLifecycleStream(t *testing.T) {
	valid := WindowInfo{ID: "17", Title: "Private report"}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
	}{
		{"missing signal", nil},
		{"foreign path", &dbus.Signal{Name: WindowsInterface + ".WindowAdded", Path: "/foreign", Body: []any{valid}}},
		{"unknown member", &dbus.Signal{Name: WindowsInterface + ".NotAnEvent", Path: dbus.ObjectPath(ObjectPath), Body: []any{valid}}},
		{"missing removal", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath)}},
		{"extra removal arg", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath), Body: []any{"17", "18"}}},
		{"wrong removal arg", &dbus.Signal{Name: WindowsInterface + ".WindowRemoved", Path: dbus.ObjectPath(ObjectPath), Body: []any{int32(17)}}},
		{"missing focus", &dbus.Signal{Name: WindowsInterface + ".FocusChanged", Path: dbus.ObjectPath(ObjectPath)}},
		{"wrong focus arg", &dbus.Signal{Name: WindowsInterface + ".FocusChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{true}}},
		{"missing added window", &dbus.Signal{Name: WindowsInterface + ".WindowAdded", Path: dbus.ObjectPath(ObjectPath)}},
		{"extra changed window", &dbus.Signal{Name: WindowsInterface + ".WindowChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{valid, valid}}},
		{"wrong changed window", &dbus.Signal{Name: WindowsInterface + ".WindowChanged", Path: dbus.ObjectPath(ObjectPath), Body: []any{fmt.Sprintf("%d", 17)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := decodeWindowEvent(tc.signal)
			if ok || !reflect.DeepEqual(event, WindowEvent{}) {
				t.Fatalf("malformed event accepted: %+v %v", event, ok)
			}
		})
	}
}

func TestBridgeTypedWindowReadErrorsDoNotReturnPartialWindowMetadata(t *testing.T) {
	failure := errors.New("synthetic D-Bus provider unavailable")
	for _, tc := range []struct {
		name, method string
		read         func(*Client) (any, error)
	}{
		{"window list", "ListWindows", func(c *Client) (any, error) { return c.ListWindows(context.Background()) }},
		{"active window", "GetActiveWindow", func(c *Client) (any, error) { return c.GetActiveWindow(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &bridgeFailingObject{errorFor: "." + tc.method, failure: failure}
			c := &Client{obj: obj}
			value, err := tc.read(c)
			if !errors.Is(err, failure) {
				t.Fatalf("provider failure lost: %v", err)
			}
			switch returned := value.(type) {
			case []WindowInfo:
				if returned != nil {
					t.Fatalf("failed read exposed list: %+v", returned)
				}
			case WindowInfo:
				if returned.ID != "" {
					t.Fatalf("failed read exposed window: %+v", returned)
				}
			default:
				t.Fatalf("unexpected read result %T", value)
			}
		})
	}
}

func startPrivateBridgeBus(t *testing.T) string {
	t.Helper()
	path := testutil.ShortSocketPath(t, "bus")
	address := "unix:path=" + path
	var stderr bytes.Buffer
	cmd := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Skipf("private dbus daemon unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	// Socket creation can precede listen(2); readiness requires completing
	// a private D-Bus authentication handshake, not just stat(2).
	deadline := time.Now().Add(2 * time.Second)
	for {
		attemptCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		conn, err := dbusutil.ConnectContext(attemptCtx, address)
		cancel()
		if err == nil {
			_ = conn.Close()
			return address
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("private D-Bus startup did not become ready: %v (%s)", err, stderr.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type syntheticGNOMECore struct {
	protocol uint32
	caps     []string
}

func (s *syntheticGNOMECore) GetProtocolVersion() (uint32, *dbus.Error) { return s.protocol, nil }
func (*syntheticGNOMECore) GetExtensionVersion() (string, *dbus.Error)  { return "1.42-synthetic", nil }
func (*syntheticGNOMECore) GetShellVersion() (string, *dbus.Error)      { return "51-synthetic", nil }
func (s *syntheticGNOMECore) GetCapabilities() ([]string, *dbus.Error) {
	return append([]string(nil), s.caps...), nil
}
func (*syntheticGNOMECore) Ping() *dbus.Error { return nil }

type syntheticGNOMEWindows struct{}

func (*syntheticGNOMEWindows) ListWindows() ([]WindowInfo, *dbus.Error) {
	return []WindowInfo{{ID: "7", Title: "Synthetic Editor", AppID: "org.example.Editor", PID: 117, Width: 800, Height: 600}}, nil
}
func (*syntheticGNOMEWindows) GetWindow(id string) (WindowInfo, *dbus.Error) {
	if id != "7" {
		return WindowInfo{}, dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"synthetic missing window"})
	}
	return WindowInfo{ID: id, Title: "Synthetic Editor", Width: 800, Height: 600}, nil
}
func (*syntheticGNOMEWindows) GetActiveWindow() (WindowInfo, *dbus.Error) {
	return WindowInfo{ID: "7", Active: true}, nil
}
func (*syntheticGNOMEWindows) Move(id string, x, y int32) *dbus.Error {
	if id != "7" {
		return dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"synthetic missing window"})
	}
	return nil
}

type syntheticGNOMEClipboard struct {
	mu   sync.Mutex
	text string
}

func (s *syntheticGNOMEClipboard) GetText() (string, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text, nil
}
func (s *syntheticGNOMEClipboard) SetText(value string) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text = value
	return nil
}
func publishSyntheticGNOME(t *testing.T, address string, protocol uint32) {
	t.Helper()
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private GNOME service: %v %v", reply, err)
	}
	path := dbus.ObjectPath(ObjectPath)
	for _, tc := range []struct {
		iface string
		impl  any
	}{
		{CoreInterface, &syntheticGNOMECore{protocol: protocol, caps: []string{CapabilityWindows, CapabilityClipboard}}},
		{WindowsInterface, &syntheticGNOMEWindows{}},
		{ClipboardInterface, &syntheticGNOMEClipboard{text: "initial-private-clipboard"}},
	} {
		if err := conn.Export(tc.impl, path, tc.iface); err != nil {
			t.Fatalf("export GNOME %s: %v", tc.iface, err)
		}
	}
}
func TestGNOMEBridgePrivateDBusNegotiationWindowsClipboardAndTypedErrors(t *testing.T) {
	address := startPrivateBridgeBus(t)
	publishSyntheticGNOME(t, address, ProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClientForBus(ctx, address)
	if err != nil {
		t.Fatalf("private GNOME service refused: %v", err)
	}
	if client.ProtocolVersion() != ProtocolVersion || client.ExtensionVersion() != "1.42-synthetic" || client.ShellVersion() != "51-synthetic" {
		t.Fatalf("protocol negotiation lost versions: %v %q %q", client.ProtocolVersion(), client.ExtensionVersion(), client.ShellVersion())
	}
	if !client.HasCapability(CapabilityClipboard) || client.HasCapability(CapabilityScreen) {
		t.Fatalf("incorrect private capabilities %v", client.Capabilities())
	}
	if lintShadow139 := client.Ping(ctx); lintShadow139 != nil {
		t.Fatal(lintShadow139)
	}
	windows, err := client.ListWindows(ctx)
	if err != nil || len(windows) != 1 || windows[0].ID != "7" || windows[0].PID != 117 || windows[0].Title != "Synthetic Editor" {
		t.Fatalf("typed GNOME window list %+v %v", windows, err)
	}
	win, err := client.GetWindow(ctx, "7")
	if err != nil || win.ID != "7" || win.Width != 800 {
		t.Fatalf("window lookup %+v %v", win, err)
	}
	active, err := client.GetActiveWindow(ctx)
	if err != nil || active.ID != "7" || !active.Active {
		t.Fatalf("active window %+v %v", active, err)
	}
	if err := client.Move(ctx, "7", 12, 34); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetWindow(ctx, "absent"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GNOME D-Bus NotFound was not mapped: %v", err)
	}
	if err := client.Move(ctx, "absent", 12, 34); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GNOME D-Bus mutator NotFound was not mapped: %v", err)
	}
	if content, err := client.GetText(ctx); err != nil || content != "initial-private-clipboard" {
		t.Fatalf("private clipboard read %q %v", content, err)
	}
	if err := client.SetText(ctx, "non-public synthetic string"); err != nil {
		t.Fatal(err)
	}
	if content, err := client.GetText(ctx); err != nil || content != "non-public synthetic string" {
		t.Fatalf("clipboard write/read %q %v", content, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed bridge still connected: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}
}
func TestGNOMEBridgePrivateBusRejectsAbsentServiceAndProtocolMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		serve   bool
		version uint32
	}{
		{"missing service", false, 0}, {"incompatible extension", true, ProtocolVersion + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := startPrivateBridgeBus(t)
			if tc.serve {
				publishSyntheticGNOME(t, address, tc.version)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, err := NewClientForBus(ctx, address)
			if client != nil || err == nil {
				t.Fatalf("invalid GNOME service accepted: %v %v", client, err)
			}
			if !tc.serve && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("missing service: %v", err)
			}
			if tc.serve {
				var mismatch *ProtocolError
				if !errors.As(err, &mismatch) {
					t.Fatalf("protocol mismatch not preserved: %v", err)
				}
			}
		})
	}
}
func TestGNOMEBridgePrivateBusMissingMethodFailsWithoutDefaultingValues(t *testing.T) {
	address := startPrivateBridgeBus(t)
	publishSyntheticGNOME(t, address, ProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClientForBus(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, _, err := client.PointerLocation(ctx); !errors.Is(err, ErrUnavailable) {
		var dbusError *dbus.Error
		errors.As(err, &dbusError)
		if dbusError != nil {
			t.Logf("missing-interface error name=%q body=%v", dbusError.Name, dbusError.Body)
		}
		t.Fatalf("missing GNOME input interface fabricated pointer: %T %v", err, err)
	}
	if _, err := client.CaptureFull(ctx, 4); err == nil || (!errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrUnixFDUnsupported)) {
		t.Fatalf("missing GNOME screen interface returned success: %v", err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("missing optional interface corrupted core: %v", err)
	}
}

// fakeSubscription stands in for the bus connection so the signal
// subscription lifecycle can be driven without a session bus.
type fakeSubscription struct {
	mu          sync.Mutex
	addCtx      context.Context //nolint:containedctx // the fake records the observed handshake context so the test can assert its deadline.
	addCalls    int
	removeCalls int
	closed      bool
	signal      chan<- *dbus.Signal
}

func (f *fakeSubscription) AddMatchSignalContext(ctx context.Context, _ ...dbus.MatchOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCtx = ctx
	f.addCalls++
	return nil
}

func (f *fakeSubscription) RemoveMatchSignalContext(_ context.Context, _ ...dbus.MatchOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	return nil
}

func (f *fakeSubscription) Signal(ch chan<- *dbus.Signal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signal = ch
}

func (f *fakeSubscription) RemoveSignal(_ chan<- *dbus.Signal) {}

func (f *fakeSubscription) SupportsUnixFDs() bool { return true }

func (f *fakeSubscription) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSubscription) deliver(t *testing.T, signal *dbus.Signal) {
	t.Helper()
	f.mu.Lock()
	ch := f.signal
	f.mu.Unlock()
	if ch == nil {
		t.Fatal("no signal channel registered")
	}
	select {
	case ch <- signal:
	case <-time.After(5 * time.Second):
		t.Fatal("signal channel not accepted")
	}
}

func (f *fakeSubscription) counts() (add, remove int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addCalls, f.removeCalls
}

func (f *fakeSubscription) handshakeContext() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addCtx
}

func newSubscriptionClient(sub signalSubscription) *Client {
	return &Client{conn: sub, closeDone: make(chan struct{})}
}

func windowAddedSignal(id, title string) *dbus.Signal {
	return &dbus.Signal{
		Path: dbus.ObjectPath(ObjectPath),
		Name: WindowsInterface + ".WindowAdded",
		Body: []any{WindowInfo{ID: id, Title: title}},
	}
}

func focusChangedSignal(id string) *dbus.Signal {
	return &dbus.Signal{
		Path: dbus.ObjectPath(ObjectPath),
		Name: WindowsInterface + ".FocusChanged",
		Body: []any{id},
	}
}

// Open hands capability constructors a startup-policy deadline context and
// cancels it as soon as capability setup returns. A subscription established
// with that context must keep serving the session it belongs to.
func TestSubscribeWindowEventsSurvivesSetupContextCancellation(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Hour)
	events, cancelSub, err := client.SubscribeWindowEvents(setupCtx)
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	// Capability setup finishes and releases its context.
	cancelSetup()
	if err := setupCtx.Err(); err == nil {
		t.Fatal("setup context is still live")
	}

	sub.deliver(t, windowAddedSignal("17", "Terminal"))
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("window event stream closed when the setup context was cancelled")
		}
		if event.Kind != WindowAddedEvent || event.ID != "17" || event.Window.Title != "Terminal" {
			t.Fatalf("event = %+v, want window-added for 17/Terminal", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no window event after the setup context was cancelled")
	}

	sub.deliver(t, focusChangedSignal("17"))
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("window event stream closed when the setup context was cancelled")
		}
		if event.Kind != FocusChangedEvent || event.ID != "17" {
			t.Fatalf("event = %+v, want focus-changed for 17", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no focus event after the setup context was cancelled")
	}
}

// The registration handshake must stay bounded by the caller's startup policy
// even though it no longer governs the stream.
func TestSubscribeWindowEventsRegistrationHonorsCallerDeadline(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Hour)
	defer cancelSetup()
	if _, cancelSub, err := client.SubscribeWindowEvents(setupCtx); err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	} else {
		defer cancelSub()
	}

	handshake := sub.handshakeContext()
	if handshake == nil {
		t.Fatal("registration handshake did not run")
	}
	deadline, ok := handshake.Deadline()
	if !ok {
		t.Fatal("registration handshake has no deadline")
	}
	want, _ := setupCtx.Deadline()
	if !deadline.Equal(want) {
		t.Fatalf("handshake deadline = %v, want the caller startup deadline %v", deadline, want)
	}
}

// A caller without a deadline still gets a finite registration ceiling.
func TestSubscribeWindowEventsRegistrationBoundsUndeadlinedCaller(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	_, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	handshake := sub.handshakeContext()
	if handshake == nil {
		t.Fatal("registration handshake did not run")
	}
	if _, ok := handshake.Deadline(); !ok {
		t.Fatal("registration handshake has no deadline")
	}
}

// Cancelling the subscription ends the stream and releases the match rule.
func TestSubscribeWindowEventsCancelClosesStreamAndRemovesMatch(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	events, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}

	cancelSub()
	cancelSub() // idempotent
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("window event stream still open after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window event stream not closed after cancel")
	}

	add, remove := sub.counts()
	if add != 1 || remove != 1 {
		t.Fatalf("match registrations = add %d remove %d, want 1/1", add, remove)
	}
}

// The client owns its subscriptions: closing it ends every stream even though
// the subscriber never cancels.
func TestSubscribeWindowEventsClientCloseClosesStream(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	events, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("window event stream still open after client close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window event stream not closed after client close")
	}
}

// A closed client cannot start a subscription at all.
func TestSubscribeWindowEventsRejectsClosedClient(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if events, cancelSub, err := client.SubscribeWindowEvents(context.Background()); err == nil {
		cancelSub()
		_ = events
		t.Fatal("SubscribeWindowEvents succeeded on a closed client")
	}
}

type recordingObject struct {
	mu                 sync.Mutex
	protocol           uint32
	calls              []recordedCall
	pointerLocationErr error
}

type blockingObject struct {
	seen chan context.Context
}

func (o *blockingObject) CallWithContext(ctx context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	o.seen <- ctx
	<-ctx.Done()
	return &dbus.Call{Err: ctx.Err()}
}

type recordedCall struct {
	method string
	args   []any
}

func (o *recordingObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	if err := ctx.Err(); err != nil {
		return &dbus.Call{Err: err}
	}
	o.mu.Lock()
	o.calls = append(o.calls, recordedCall{method: method, args: args})
	o.mu.Unlock()
	call := &dbus.Call{}
	switch method {
	case CoreInterface + ".GetProtocolVersion":
		call.Body = []any{o.protocol}
	case CoreInterface + ".GetExtensionVersion":
		call.Body = []any{ExtensionVersion}
	case CoreInterface + ".GetShellVersion":
		call.Body = []any{"51.0"}
	case CoreInterface + ".GetCapabilities":
		call.Body = []any{[]string{"windows", "screen", "input", "clipboard"}}
	case WindowsInterface + ".ListWindows":
		call.Body = []any{[]WindowInfo{{ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", PID: 42, X: 1, Y: 2, Width: 800, Height: 600, Active: true}}}
	case WindowsInterface + ".GetWindow":
		call.Body = []any{WindowInfo{ID: "17", Title: "Terminal", Width: 800, Height: 600}}
	case WindowsInterface + ".GetActiveWindow":
		call.Body = []any{WindowInfo{ID: "17", Title: "Terminal", Active: true}}
	case InputInterface + ".PointerLocation":
		if o.pointerLocationErr != nil {
			return &dbus.Call{Err: o.pointerLocationErr}
		}
		call.Body = []any{int32(11), int32(22)}
	case ScreenInterface + ".CaptureFull":
		call.Body = []any{int32(0), int32(0), int32(1280), int32(720), int32(2560), int32(1440), float64(2)}
	case ScreenInterface + ".CaptureRegion":
		call.Body = []any{int32(10), int32(20), int32(100), int32(50), int32(150), int32(75), float64(1.5)}
	}
	return call
}

func newRecordingClient(t *testing.T) (*Client, *recordingObject) {
	t.Helper()
	object := &recordingObject{protocol: ProtocolVersion}
	client, err := newClientWithObject(context.Background(), object)
	if err != nil {
		t.Fatalf("newClientWithObject: %v", err)
	}
	return client, object
}

func TestClientNegotiates(t *testing.T) {
	client, _ := newRecordingClient(t)

	if client.ProtocolVersion() != ProtocolVersion || client.ExtensionVersion() != ExtensionVersion || client.ShellVersion() != "51.0" {
		t.Fatalf("negotiated metadata = protocol %d, extension %q, shell %q", client.ProtocolVersion(), client.ExtensionVersion(), client.ShellVersion())
	}
	caps := client.Capabilities()
	caps[0] = "changed"
	if !client.HasCapability(CapabilityScreen) || client.Capabilities()[0] == "changed" {
		t.Fatalf("Capabilities did not return a defensive copy: %v", client.Capabilities())
	}
}

func TestClientCallHonorsCallerDeadlineAndCancellationCause(t *testing.T) {
	object := &blockingObject{seen: make(chan context.Context, 1)}
	client := &Client{obj: object, closeDone: make(chan struct{})}
	parent, cancelCause := context.WithCancelCause(context.Background())
	ctx, deadlineCancel := context.WithTimeout(parent, time.Second)
	defer deadlineCancel()
	result := make(chan error, 1)
	go func() { result <- client.call(ctx, CoreInterface, "Ping") }()
	observed := <-object.seen
	deadline, ok := observed.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
		t.Fatalf("caller deadline = %v, ok=%v; want preserved deadline", deadline, ok)
	}
	cause := errors.New("caller stopped DBus operation")
	cancelCause(cause)
	if err := <-result; !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("call error = %v, want cancellation and caller cause", err)
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer deadlineCancel()
	result = make(chan error, 1)
	go func() { result <- client.call(deadlineCtx, CoreInterface, "Ping") }()
	observed = <-object.seen
	deadline, ok = observed.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 5*time.Millisecond {
		t.Fatalf("caller deadline = %v, ok=%v; want preserved deadline", deadline, ok)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline call error = %v, want context deadline", err)
	}
}

func TestClientUsesTypedMethods(t *testing.T) {
	client, object := newRecordingClient(t)

	windows, err := client.ListWindows(context.Background())
	if err != nil || len(windows) != 1 || windows[0].ID != "17" {
		t.Fatalf("ListWindows = %#v, %v", windows, err)
	}
	window, err := client.GetWindow(context.Background(), "17")
	if err != nil || window.ID != "17" {
		t.Fatalf("GetWindow = %#v, %v", window, err)
	}
	if moveErr := client.Move(context.Background(), "17", 4, 5); moveErr != nil {
		t.Fatalf("Move: %v", moveErr)
	}
	x, y, err := client.PointerLocation(context.Background())
	if err != nil || x != 11 || y != 22 {
		t.Fatalf("PointerLocation = %d,%d,%v", x, y, err)
	}
	if err := client.SetText(context.Background(), "hello"); err != nil {
		t.Fatalf("SetText: %v", err)
	}
	if err := client.Paste(context.Background(), "é"); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	assertRecordedMove(t, object)
}

func TestClientCaptureReturnsLogicalAndPixelGeometry(t *testing.T) {
	client, _ := newRecordingClient(t)

	full, err := client.CaptureFull(context.Background(), 9)
	if err != nil {
		t.Fatalf("CaptureFull: %v", err)
	}
	wantFull := ScreenCapture{
		Width:       1280,
		Height:      720,
		PixelWidth:  2560,
		PixelHeight: 1440,
		Scale:       2,
	}
	if !reflect.DeepEqual(full, wantFull) {
		t.Fatalf("CaptureFull geometry = %#v, want %#v", full, wantFull)
	}

	region, err := client.CaptureRegion(context.Background(), 9, image.Rect(10, 20, 110, 70))
	if err != nil {
		t.Fatalf("CaptureRegion: %v", err)
	}
	wantRegion := ScreenCapture{
		X:           10,
		Y:           20,
		Width:       100,
		Height:      50,
		PixelWidth:  150,
		PixelHeight: 75,
		Scale:       1.5,
	}
	if !reflect.DeepEqual(region, wantRegion) {
		t.Fatalf("CaptureRegion geometry = %#v, want %#v", region, wantRegion)
	}
}

func assertRecordedMove(t *testing.T, object *recordingObject) {
	t.Helper()
	object.mu.Lock()
	defer object.mu.Unlock()
	for _, call := range object.calls {
		if call.method == WindowsInterface+".Move" {
			if !reflect.DeepEqual(call.args, []any{"17", int32(4), int32(5)}) {
				t.Fatalf("Move call = %#v, want typed args", call)
			}
			return
		}
	}
	t.Fatal("Move call was not recorded")
}

func TestClientRejectsProtocolMismatch(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion + 1}
	_, err := newClientWithObject(context.Background(), object)
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("newClientWithObject error = %v, want protocol mismatch", err)
	}
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("error = %v, want ErrProtocolMismatch", err)
	}
}

func TestDecodeWindowEvent(t *testing.T) {
	window := []any{"17", "Terminal", "org.gnome.Terminal", "Terminal", int32(42), int32(1), int32(2), int32(800), int32(600), true, false, true, false}
	tests := []struct {
		name string
		sig  *dbus.Signal
		want WindowEvent
	}{
		{
			name: "added",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".WindowAdded", Body: []any{window}},
			want: WindowEvent{Kind: WindowAddedEvent, ID: "17", Window: WindowInfo{ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", Class: "Terminal", PID: 42, X: 1, Y: 2, Width: 800, Height: 600, Active: true, Maximized: true}},
		},
		{
			name: "removed",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".WindowRemoved", Body: []any{"17"}},
			want: WindowEvent{Kind: WindowRemovedEvent, ID: "17"},
		},
		{
			name: "focus",
			sig:  &dbus.Signal{Path: dbus.ObjectPath(ObjectPath), Name: WindowsInterface + ".FocusChanged", Body: []any{"17"}},
			want: WindowEvent{Kind: FocusChangedEvent, ID: "17"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := decodeWindowEvent(test.sig)
			if !ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("decodeWindowEvent = %#v, %v; want %#v, true", got, ok, test.want)
			}
		})
	}
	if _, ok := decodeWindowEvent(&dbus.Signal{Path: dbus.ObjectPath("/wrong"), Name: WindowsInterface + ".WindowRemoved", Body: []any{"17"}}); ok {
		t.Fatal("decodeWindowEvent accepted a signal from the wrong path")
	}
}

func TestEnqueueWindowEventIsBoundedAndCoalescesChanges(t *testing.T) {
	queue := []WindowEvent{
		{Kind: WindowChangedEvent, ID: "17", Window: WindowInfo{ID: "17", Title: "old"}},
	}
	queue = enqueueWindowEvent(queue, WindowEvent{Kind: WindowChangedEvent, ID: "17", Window: WindowInfo{ID: "17", Title: "new"}})
	if len(queue) != 1 || queue[0].Window.Title != "new" {
		t.Fatalf("coalesced window changes = %#v, want latest single event", queue)
	}
	for i := 0; i < maxWindowEventQueue; i++ {
		queue = enqueueWindowEvent(queue, WindowEvent{Kind: WindowAddedEvent, ID: fmt.Sprint(i)})
	}
	if len(queue) != maxWindowEventQueue {
		t.Fatalf("event queue length = %d, want bound %d", len(queue), maxWindowEventQueue)
	}
	queue = enqueueWindowEvent(queue, WindowEvent{Kind: WindowAddedEvent, ID: "overflow"})
	if len(queue) != maxWindowEventQueue {
		t.Fatalf("event queue grew to %d past bound %d", len(queue), maxWindowEventQueue)
	}
}

func TestTranslateDBusError(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
	}{
		{name: "org.freedesktop.DBus.Error.UnknownObject", want: ErrUnavailable},
		{name: "org.freedesktop.DBus.Error.ServiceUnknown", want: ErrUnavailable},
		{name: "io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", want: ErrObjectNotFound},
		{name: "io.github.nskaggs.perfuncted.Gnome1.Error.Unsupported", want: ErrUnavailable},
	} {
		err := translateDBusError(dbus.NewError(test.name, []any{"test"}))
		if !errors.Is(err, test.want) {
			t.Errorf("translateDBusError(%q) = %v, want %v", test.name, err, test.want)
		}
	}
}

func TestPointerLocationTranslatesTypedUnsupportedError(t *testing.T) {
	client, object := newRecordingClient(t)
	object.pointerLocationErr = dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.Unsupported", []any{"GNOME pointer location is unavailable"})
	_, _, err := client.PointerLocation(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("PointerLocation error = %v, want typed bridge unsupported translated to ErrUnavailable", err)
	}
	if !strings.Contains(err.Error(), "GNOME pointer location is unavailable") {
		t.Fatalf("PointerLocation error = %v, missing bridge diagnostic", err)
	}
}

func TestClientCancellationAndClose(t *testing.T) {
	object := &recordingObject{protocol: ProtocolVersion}
	client, err := newClientWithObject(context.Background(), object)
	if err != nil {
		t.Fatalf("newClientWithObject: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Ping(canceled); err == nil {
		t.Fatal("Ping with canceled context succeeded")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := client.Ping(context.Background()); err == nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ping after Close = %v, want ErrUnavailable", err)
	}
}

// Older GNOME extensions may return an untyped D-Bus error whose message
// identifies a missing window. Preserve the original error while translating
// only that specific failure into the public object-not-found sentinel.
func TestGNOMEUnknownDBusErrorWindowIdentityFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		message      string
		wantNotFound bool
	}{
		{"missing window lowercase", "window not found", true},
		{"missing window mixed case", "Window Not Found: 42", true},
		{"unrelated permission rejection", "permission denied", false},
		{"similar but distinct error", "window unavailable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := dbus.NewError("org.example.LegacyExtension.Error", []any{tc.message})
			for _, err := range []error{original, *original, fmt.Errorf("wrapped error: %w", *original)} {
				translated := translateDBusError(err)
				if errors.Is(translated, ErrObjectNotFound) != tc.wantNotFound {
					t.Fatalf("incorrect not-found classification for %T %q: %v", err, tc.message, translated)
				}
				if !strings.Contains(translated.Error(), tc.message) {
					t.Fatalf("lost original D-Bus error for %T %q: %v", err, tc.message, translated)
				}
				if errors.Is(translated, ErrUnavailable) {
					t.Fatalf("legacy extension error unexpectedly classified as unavailable: %v", translated)
				}
			}
		})
	}
}
