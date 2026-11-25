package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xtest"
	"github.com/nskaggs/perfuncted/internal/x11"
	"github.com/nskaggs/perfuncted/internal/x11test"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type mockX11Options struct {
	events        string
	afterRegister <-chan struct{}
	xwayland      bool
	noXI          bool
	version       uint16
	noKey         bool
	noModifiers   bool
	badSetup      bool
	badKeycodes   bool
	onAction      func(string)
	mapReplies    int
	selectError   byte
	disconnectOn  string
	cancelOn      string
	cancel        context.CancelFunc
}

func startMockXI2Server(t *testing.T, options mockX11Options) string {
	t.Helper()
	display := shortPrivateSocketPath(t, "x11") + ":0"
	listener, err := net.Listen("unix", display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		if err := mockX11SetupWithRoots(connection, !options.badSetup, options.badKeycodes); err != nil {
			return
		}
		sequence := uint16(0)
		for {
			header := make([]byte, 4)
			if _, err := io.ReadFull(connection, header); err != nil {
				return
			}
			length := int(xgb.Get16(header[2:4]))
			if length < 1 || length > 4096 {
				return
			}
			req := make([]byte, length*4)
			copy(req, header)
			if _, err := io.ReadFull(connection, req[4:]); err != nil {
				return
			}
			sequence++
			action := ""
			switch {
			case header[0] == 98 && len(req) >= 8:
				nameLength := int(xgb.Get16(req[4:6]))
				if 8+nameLength > len(req) {
					return
				}
				action = string(req[8 : 8+nameLength])
			case header[0] == 131 && header[1] == 47:
				action = "XIQueryVersion"
			case header[0] == 131 && header[1] == 46:
				action = "XISelectEvents"
			case header[0] == 101:
				action = "GetKeyboardMapping"
			case header[0] == 43:
				action = "GetInputFocus"
			default:
				return
			}
			if options.onAction != nil {
				options.onAction(action)
			}
			if options.cancelOn == action && options.cancel != nil {
				options.cancel()
			}
			if options.disconnectOn == action {
				return
			}
			reply := make([]byte, 32)
			reply[0] = 1
			xgb.Put16(reply[2:4], sequence)
			switch action {
			case "XWAYLAND":
				if options.xwayland {
					reply[8] = 1
					reply[9] = 130
				}
			case "XInputExtension":
				if !options.noXI {
					reply[8] = 1
					reply[9] = 131
				}
			case "XIQueryVersion":
				major := uint16(2)
				if options.version > 0 {
					major = options.version
				}
				xgb.Put16(reply[8:10], major)
				xgb.Put16(reply[10:12], 1)
			case "XISelectEvents":
				if options.selectError != 0 {
					reply[0], reply[1] = 0, options.selectError
					if _, err := connection.Write(reply); err != nil {
						return
					}
				}
				continue
			case "GetKeyboardMapping":
				options.mapReplies++
				if options.disconnectOn == "second-mapping" && options.mapReplies == 2 {
					return
				}
				first, count := int(req[4]), int(req[5])
				reply[1] = 1
				keysyms := make([]byte, count*4)
				for i := 0; i < count; i++ {
					key := first + i
					var keysym uint32
					switch key {
					case 72:
						if !options.noKey {
							keysym = 0xffc5
						}
					case 37:
						if !options.noModifiers {
							keysym = 0xffe3
						}
					case 50:
						if !options.noModifiers {
							keysym = 0xffe1
						}
					case 64:
						keysym = 0xffe9
					case 133:
						keysym = 0xffeb
					}
					xgb.Put32(keysyms[i*4:], keysym)
				}
				xgb.Put32(reply[4:8], uint32(count))
				reply = append(reply, keysyms...)
			case "GetInputFocus":
				reply[1] = 1
			}
			if _, err := connection.Write(reply); err != nil {
				return
			}
			if action == "GetInputFocus" && options.disconnectOn == "after-register" {
				if options.afterRegister != nil {
					<-options.afterRegister
				}
				return
			}
			if action == "GetInputFocus" && options.events != "" {
				if err := sendMockXI2Events(connection, options.events); err != nil {
					return
				}
			}
		}
	}()
	return display
}

func mockX11SetupWithRoots(conn net.Conn, roots, badKeycodes bool) error {
	initial := make([]byte, 12)
	if _, err := io.ReadFull(conn, initial); err != nil {
		return err
	}
	authSize := xgb.Pad(int(xgb.Get16(initial[6:8]))) + xgb.Pad(int(xgb.Get16(initial[8:10])))
	if _, err := io.CopyN(io.Discard, conn, int64(authSize)); err != nil {
		return err
	}
	rootCount := byte(0)
	var rootList []xproto.ScreenInfo
	if roots {
		rootCount = 1
		rootList = []xproto.ScreenInfo{{Root: 1, WidthInPixels: 1024, HeightInPixels: 768}}
	}
	minKey, maxKey := xproto.Keycode(8), xproto.Keycode(255)
	if badKeycodes {
		minKey, maxKey = 255, 8
	}
	info := xproto.SetupInfo{
		Status: 1, ProtocolMajorVersion: 11, ProtocolMinorVersion: 0,
		ResourceIdBase: 0x1000000, ResourceIdMask: 0x3ffff,
		MaximumRequestLength: 65535, RootsLen: rootCount, MinKeycode: minKey, MaxKeycode: maxKey,
		Roots: rootList,
	}
	reply := info.Bytes()
	xgb.Put16(reply[6:8], uint16((len(reply)-8)/4))
	_, err := conn.Write(reply)
	return err
}

func TestMockXI2ExtensionVersionAndCancellationFailures(t *testing.T) {
	cases := []struct {
		name   string
		config mockX11Options
		want   error
	}{
		{"xwayland", mockX11Options{xwayland: true}, ErrObservationUnsupported},
		{"no-xi", mockX11Options{noXI: true}, ErrObservationUnsupported},
		{"old-version", mockX11Options{version: 1}, ErrObservationUnsupported},
		{"disconnect-wayland", mockX11Options{disconnectOn: "XWAYLAND"}, ErrObservationUnavailable},
		{"disconnect-xi", mockX11Options{disconnectOn: "XInputExtension"}, ErrObservationUnavailable},
		{"disconnect-version", mockX11Options{disconnectOn: "XIQueryVersion"}, ErrObservationUnavailable},
		{"cancel-wayland", mockX11Options{cancelOn: "XWAYLAND"}, context.Canceled},
		{"cancel-xi", mockX11Options{cancelOn: "XInputExtension"}, context.Canceled},
		{"cancel-version", mockX11Options{cancelOn: "XIQueryVersion"}, context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			tc.config.cancel = cancel
			display := startMockXI2Server(t, tc.config)
			conn, _, err := openXI2Connection(ctx, display)
			if conn != nil {
				conn.Abort()
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestMockXI2ListenAdmissionAndFailures(t *testing.T) {
	cases := []struct {
		name   string
		config mockX11Options
		filter ObservationFilter
		want   error
	}{
		{"valid-button", mockX11Options{}, ObservationFilter{Button: 1}, nil},
		{"valid-hotkey", mockX11Options{}, ObservationFilter{Combo: "Ctrl+F8"}, nil},
		{"missing-key", mockX11Options{noKey: true}, ObservationFilter{Combo: "F8"}, ErrObservationUnsupported},
		{"missing-modifier", mockX11Options{noModifiers: true}, ObservationFilter{Combo: "Ctrl+F8"}, ErrObservationUnsupported},
		{"map-transport-error", mockX11Options{disconnectOn: "GetKeyboardMapping"}, ObservationFilter{Combo: "F8"}, ErrObservationUnavailable},
		{"invalid-select", mockX11Options{selectError: 3}, ObservationFilter{Button: 1}, ErrObservationUnavailable},
		{"denied-select", mockX11Options{selectError: 10}, ObservationFilter{Button: 1}, ErrObservationDenied},
		{"cancel-map", mockX11Options{cancelOn: "GetKeyboardMapping"}, ObservationFilter{Combo: "F8"}, context.Canceled},
		{"cancel-select", mockX11Options{cancelOn: "XISelectEvents"}, ObservationFilter{Button: 1}, context.Canceled},
		{"no-roots", mockX11Options{badSetup: true}, ObservationFilter{Button: 1}, ErrObservationUnavailable},
		{"bad-keycodes", mockX11Options{badKeycodes: true}, ObservationFilter{Combo: "F8"}, ErrObservationUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			tc.config.cancel = cancel
			display := startMockXI2Server(t, tc.config)
			backend := &x11Observer{display: display, subscriptions: make(map[*observationSubscription]struct{})}
			sub, err := backend.Listen(ctx, tc.filter)
			if !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
			if sub != nil {
				_ = sub.Close()
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMockXI2ListenersRejectInvalidAndCanceled(t *testing.T) {
	backend := &x11Observer{display: "invalid", subscriptions: make(map[*observationSubscription]struct{})}
	if _, err := backend.Listen(context.Background(), ObservationFilter{}); !errors.Is(err, ErrInvalidObservationFilter) {
		t.Fatal(err)
	}
	if _, err := backend.Listen(nil, ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrInvalidObservationFilter) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Listen(canceled, ObservationFilter{Button: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := backend.Listen(context.Background(), ObservationFilter{Button: 1}); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	_ = backend.Close()
	if _, err := backend.Listen(context.Background(), ObservationFilter{Button: 1}); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
	if ops := backend.SupportedOperations(); len(ops) != 5 {
		t.Fatalf("operations %v", ops)
	}
}

// These are genuine XI2 GenericEvent wire frames on a fake unprivileged
// server. They exercise event-source authority without root or evdev.
func sendMockXI2Events(w io.Writer, mode string) error {
	send := func(b []byte) error { _, err := w.Write(b); return err }
	switch mode {
	case "held-map":
		if err := send(rawXI2(13, 72, 131, 1, 1)); err != nil {
			return err
		}
		notify := make([]byte, 32)
		notify[0] = 34 // core MappingNotify
		return send(notify)
	case "held-hierarchy":
		if err := send(rawXI2(13, 72, 131, 1, 1)); err != nil {
			return err
		}
		return send(rawXI2(11, 0, 131, 1, 1))
	case "overflow":
		for i := 0; i < 100; i++ {
			if err := send(rawXI2(15, 1, 131, 2, 2)); err != nil {
				return err
			}
			if err := send(rawXI2(16, 1, 131, 2, 2)); err != nil {
				return err
			}
		}
	case "idle-hierarchy":
		if err := send(rawXI2(11, 0, 131, 1, 1)); err != nil {
			return err
		}
		if err := send(rawXI2(15, 1, 131, 1, 1)); err != nil {
			return err
		}
		return send(rawXI2(16, 1, 131, 1, 1))
	case "core-unrelated":
		ordinary := make([]byte, 32)
		ordinary[0] = 6
		if err := send(ordinary); err != nil {
			return err
		}
		if err := send(rawXI2(15, 1, 131, 1, 1)); err != nil {
			return err
		}
		return send(rawXI2(16, 1, 131, 1, 1))
	case "stream-error":
		bad := make([]byte, 32)
		bad[0], bad[1] = 0, 3
		return send(bad)

	case "idle-map":
		notify := make([]byte, 32)
		notify[0] = 34
		return send(notify)
	case "unknown-extension":
		other := rawXI2(13, 72, 132, 1, 1)
		if err := send(other); err != nil {
			return err
		}
		if err := send(rawXI2(15, 1, 131, 1, 1)); err != nil {
			return err
		}
		return send(rawXI2(16, 1, 131, 1, 1))
	}
	return nil
}

func TestMockXI2EventStreamLossAndUnknownSources(t *testing.T) {
	cases := []struct {
		name     string
		events   string
		filter   ObservationFilter
		wantLoss bool
	}{
		{"held-keymap", "held-map", ObservationFilter{Combo: "F8", Transition: "both"}, true},
		{"held-hierarchy", "held-hierarchy", ObservationFilter{Combo: "F8", Transition: "both"}, true},
		{"overflow", "overflow", ObservationFilter{Button: 1, Transition: "both"}, true},
		{"unknown-extension", "unknown-extension", ObservationFilter{Button: 1, Transition: "both"}, false},
		{"idle-hierarchy", "idle-hierarchy", ObservationFilter{Button: 1, Transition: "both"}, false},
		{"core-unrelated", "core-unrelated", ObservationFilter{Button: 1, Transition: "both"}, false},
		{"idle-map-disconnect", "idle-map", ObservationFilter{Combo: "F8"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cfg := mockX11Options{events: tc.events}
			if tc.events == "idle-map" {
				cfg.disconnectOn = "second-mapping"
			}
			backend := &x11Observer{display: startMockXI2Server(t, cfg), subscriptions: make(map[*observationSubscription]struct{})}
			sub, err := backend.Listen(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sub.Close(); _ = backend.Close() }()
			if tc.events == "overflow" {
				// Deliberately leave the bounded queue unread while the fake
				// server delivers more than its capacity.
				until := time.Now().Add(500 * time.Millisecond)
				for !errors.Is(sub.Err(), ErrObservationLoss) && time.Now().Before(until) {
					time.Sleep(time.Millisecond)
				}
			}
			deadline := time.After(500 * time.Millisecond)
			count := 0
			for {
				select {
				case ev, ok := <-sub.Events():
					if !ok {
						if !tc.wantLoss {
							t.Fatalf("unexpected loss: %v", sub.Err())
						}
						if !errors.Is(sub.Err(), ErrObservationLoss) {
							t.Fatalf("loss not reported: %v", sub.Err())
						}
						if count == 0 && tc.events != "idle-map" && tc.events != "stream-error" {
							t.Fatal("held input/overflow never delivered")
						}
						return
					}
					if ev.Source != "x11-xi2" {
						t.Fatalf("untrusted source %v", ev)
					}
					count++
					if !tc.wantLoss && count == 2 {
						if err := sub.Close(); err != nil {
							t.Fatal(err)
						}
						return
					}
				case <-deadline:
					t.Fatalf("stream stalled after %d delivered events, err=%v", count, sub.Err())
				}
			}
		})
	}
}

func TestMockXI2DisconnectionsAndClosedDuringRegistration(t *testing.T) {
	t.Run("server-disconnects", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		release := make(chan struct{})
		defer func() {
			select {
			case <-release:
			default:
				close(release)
			}
		}()
		backend := &x11Observer{display: startMockXI2Server(t, mockX11Options{disconnectOn: "after-register", afterRegister: release}), subscriptions: make(map[*observationSubscription]struct{})}
		sub, err := backend.Listen(ctx, ObservationFilter{Button: 1})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = sub.Close(); _ = backend.Close() }()
		close(release)
		select {
		case _, ok := <-sub.Events():
			if ok {
				t.Fatal("unexpected event")
			}
			if !errors.Is(sub.Err(), ErrObservationLoss) {
				t.Fatal(sub.Err())
			}
		case <-time.After(time.Second):
			t.Fatal("disconnection not reported")
		}
	})
	t.Run("session-close-during-registration", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var backend *x11Observer
		cfg := mockX11Options{onAction: func(action string) {
			if action == "GetInputFocus" {
				_ = backend.Close()
			}
		}}
		backend = &x11Observer{display: startMockXI2Server(t, cfg), subscriptions: make(map[*observationSubscription]struct{})}
		if _, err := backend.Listen(ctx, ObservationFilter{Button: 1}); !errors.Is(err, ErrObservationRevoked) {
			t.Fatal(err)
		}
	})
}

func TestMockXI2DirectMapValidationAndChords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := openXI2Connection(ctx, startMockXI2Server(t, mockX11Options{}))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Abort()
	if _, err := loadXI2ChordMap(conn, "InvalidKey"); !errors.Is(err, ErrInvalidObservationFilter) {
		t.Fatal(err)
	}
	state := newXI2State(ObservationFilter{Combo: "Ctrl+F8", Transition: "both"},
		&xi2ChordMap{target: map[byte]bool{72: true}, modifiers: map[byte]string{37: "Alt"}, expected: map[string]bool{"Ctrl": true}})
	_, _ = state.onEvent(xi2Raw{kind: 13, detail: 37, source: 1})
	if ev, ok := state.onEvent(xi2Raw{kind: 13, detail: 72, source: 1}); ok {
		t.Fatalf("mismatched modifier incorrectly matched: %v", ev)
	}
}

func TestOpenXI2ConnectionImmediateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if conn, _, err := openXI2Connection(ctx, "no-display"); conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("early cancellation: %T %v", conn, err)
	}
}
func TestOpenX11ObserverRejectsUntrustedServer(t *testing.T) {
	if backend, err := openX11Observer(context.Background(), "remote:0"); backend != nil || !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("remote server: %T %v", backend, err)
	}
}

func TestXI2KeyboardMappingValidationWithoutServer(t *testing.T) {
	cases := []struct {
		name  string
		reply *xproto.GetKeyboardMappingReply
		combo string
		want  error
	}{
		{"nil-reply", nil, "F8", ErrObservationUnavailable},
		{"zero-stride", &xproto.GetKeyboardMappingReply{}, "F8", ErrObservationUnavailable},
		{"truncated-reply", &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1}, "F8", ErrObservationUnavailable},
		{"invalid-combo", &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0xffc5}}, "InvalidKey", ErrInvalidObservationFilter},
		{"missing-target", &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0xffe3}}, "F8", ErrObservationUnsupported},
		{"simple-valid", &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0xffc5}}, "F8", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapping, err := chordMapFromKeyboardReply(72, 72, tc.reply, tc.combo)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v expected %v", err, tc.want)
			}
			if tc.want == nil && (mapping == nil || !mapping.target[72]) {
				t.Fatalf("no target: %+v", mapping)
			}
		})
	}
}

// cancellationOnErrContext makes every X11 registration boundary testable
// without changing production code or relying on scheduler timing. It uses
// a real context.WithCancel so Done and Err agree after the trigger.
type cancellationOnErrContext struct {
	context.Context
	cancel  context.CancelFunc
	count   atomic.Int64
	trigger int64
}

func (c *cancellationOnErrContext) Err() error {
	if c.count.Add(1) == c.trigger {
		c.cancel()
	}
	return c.Context.Err()
}

func TestXI2CancellationAtEveryNegotiationBoundary(t *testing.T) {
	for trip := int64(1); trip <= 12; trip++ {
		t.Run(fmt.Sprintf("boundary-%d", trip), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrapped := &cancellationOnErrContext{Context: ctx, cancel: cancel, trigger: trip}
			display := startMockXI2Server(t, mockX11Options{})
			conn, _, err := openXI2Connection(wrapped, display)
			if conn != nil {
				conn.Abort()
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("boundary=%d err=%v calls=%d", trip, err, wrapped.count.Load())
			}
			if wrapped.Context.Err() != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost at %d: %v", trip, err)
			}
		})
	}
}

func TestXI2CancellationAtEveryListenerAdmissionBoundary(t *testing.T) {
	for trip := int64(1); trip <= 18; trip++ {
		t.Run(fmt.Sprintf("boundary-%d", trip), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wrapped := &cancellationOnErrContext{Context: ctx, cancel: cancel, trigger: trip}
			backend := &x11Observer{display: startMockXI2Server(t, mockX11Options{}), subscriptions: make(map[*observationSubscription]struct{})}
			defer backend.Close()
			sub, err := backend.Listen(wrapped, ObservationFilter{Combo: "F8"})
			if sub != nil {
				_ = sub.Close()
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("boundary=%d err=%v calls=%d", trip, err, wrapped.count.Load())
			}
		})
	}
}

func TestObservationFilterValidation(t *testing.T) {
	valid := []ObservationFilter{{Combo: "F8"}, {Combo: "ctrl+shift+p", Transition: "both"}, {Button: 1}, {Button: 3, Transition: "release"}}
	for _, tc := range valid {
		got, err := normalizeObservationFilter(tc)
		if err != nil {
			t.Fatalf("%+v: %v", tc, err)
		}
		if got.Combo == "ctrl+shift+p" {
			t.Fatal("not canonicalized")
		}
	}
	invalid := []ObservationFilter{
		{}, {Combo: "F8", Button: 1}, {Button: 4}, {Combo: ""}, {Combo: "Ctrl+"},
		{Combo: "Control+Ctrl+F8"}, {Combo: "Ctrl+Alt"}, {Combo: "F13"},
		{Combo: "{key down}"}, {Combo: "Ctrl+F8", Transition: "held"},
		{Combo: "A+B"}, {Combo: "F08"}, {Combo: "f8foo"},
	}
	for _, tc := range invalid {
		if err := ValidateObservationFilter(tc); !errors.Is(err, ErrInvalidObservationFilter) {
			t.Errorf("%+v: %v", tc, err)
		}
	}
}
func TestObservationBoundedOverflowAndCancel(t *testing.T) {
	n := 0
	sub := newObservationSubscription(func() { n++ })
	ev := ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "press", Time: time.Now()}
	for i := 0; i < observationQueueSize; i++ {
		if !sub.push(ev) {
			t.Fatalf("premature loss at %d", i)
		}
	}
	if sub.push(ev) {
		t.Fatal("overflow accepted")
	}
	if !errors.Is(sub.Err(), ErrObservationLoss) {
		t.Fatalf("overflow not surfaced: %v", sub.Err())
	}
	if n != 1 {
		t.Fatalf("cleanup count %d", n)
	}
	if sub.push(ev) {
		t.Fatal("event after closure")
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("duplicate cleanup: %d", n)
	}
	count := 0
	for range sub.Events() {
		count++
	}
	if count != observationQueueSize {
		t.Fatalf("buffered events %d", count)
	}
	other := newObservationSubscription(func() { n++ })
	other.finish(context.Canceled)
	if !errors.Is(other.Err(), context.Canceled) {
		t.Fatal(other.Err())
	}
	if n != 2 {
		t.Fatalf("cleanup count %d", n)
	}
}
func TestObservationRoutingRejectsWaylandAndUnknown(t *testing.T) {
	for _, vars := range [][]string{
		{"XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=GNOME", "DISPLAY=:0"},
		{"XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway", "DISPLAY=:0"},
		{"XDG_SESSION_TYPE=unknown", "DISPLAY=:0"},
		{"XDG_SESSION_TYPE=x11"},
	} {
		observer, err := OpenObserver(context.Background(), env.FromEnviron(vars))
		if observer != nil || !errors.Is(err, ErrObservationUnsupported) {
			t.Fatalf("%v: %T %v", vars, observer, err)
		}
	}
	if _, err := OpenObserver(nil, env.Current()); !errors.Is(err, ErrInvalidObservationFilter) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenObserver(ctx, env.Current()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func rawXI2(kind uint16, detail uint32, opcode byte, device, source uint16) []byte {
	b := make([]byte, 32)
	b[0], b[1] = 35, opcode
	xgb.Put16(b[8:10], kind)
	xgb.Put16(b[10:12], device)
	xgb.Put32(b[16:20], detail)
	xgb.Put16(b[20:22], source)
	return b
}
func TestXI2RawDecoderAndProvenance(t *testing.T) {
	valid := rawXI2(13, 72, 131, 1, 3)
	event, ok, err := decodeXI2Raw(valid, 131)
	if err != nil || !ok || event.kind != 13 || event.device != 1 || event.source != 3 || event.detail != 72 {
		t.Fatalf("%+v %v %v", event, ok, err)
	}
	if _, ok, _ = decodeXI2Raw(valid, 132); ok {
		t.Fatal("wrong extension accepted")
	}
	if _, ok, _ = decodeXI2Raw(rawXI2(99, 72, 131, 1, 3), 131); ok {
		t.Fatal("unknown event accepted")
	}
	if _, _, err := decodeXI2Raw(valid[:30], 131); err == nil {
		t.Fatal("short event accepted")
	}
	malformed := append([]byte{}, valid...)
	xgb.Put32(malformed[4:8], 2)
	if _, _, err := decodeXI2Raw(malformed, 131); err == nil {
		t.Fatal("unframed event accepted")
	}
	if _, ok, _ = decodeXI2Raw(rawXI2(11, 0, 131, 1, 3), 131); !ok {
		t.Fatal("hierarchy loss ignored")
	}
}
func TestXI2ChordStateAndMultipleDevices(t *testing.T) {
	mapping := &xi2ChordMap{
		target:    map[byte]bool{72: true},
		modifiers: map[byte]string{37: "Ctrl", 105: "Ctrl", 50: "Shift"},
		expected:  map[string]bool{"Ctrl": true},
	}
	state := newXI2State(ObservationFilter{Combo: "Ctrl+F8", Transition: "both"}, mapping)
	step := func(kind uint16, detail uint32, source uint16, want bool, transition string) {
		t.Helper()
		event, ok := state.onEvent(xi2Raw{kind: kind, detail: detail, source: source})
		if ok != want {
			t.Fatalf("kind %d key %d source %d: got %v %+v want %v", kind, detail, source, ok, event, want)
		}
		if want && (event.Transition != transition || event.Source != "x11-xi2" || event.ID != "Ctrl+F8") {
			t.Fatalf("event %+v", event)
		}
	}
	step(13, 37, 1, false, "")
	step(13, 72, 2, false, "") // never combine different keyboards
	step(13, 72, 1, true, "press")
	step(13, 72, 1, false, "") // repeat suppressed
	step(13, 37, 2, false, "") // second source also becomes active
	step(14, 37, 1, false, "") // second source still holds chord
	step(14, 72, 1, false, "")
	step(14, 37, 2, true, "release")
	step(14, 72, 2, false, "")
	step(13, 72, 1, false, "")
	step(13, 37, 1, true, "press") // modifier pressed after target on same keyboard
	step(14, 72, 1, true, "release")
	step(14, 37, 1, false, "")
	repeat := xi2Raw{kind: 13, detail: 72, source: 1, flags: 1 << 16}
	if got, ok := state.onEvent(repeat); ok {
		t.Fatalf("unknown repeat creates press %+v", got)
	}
}
func TestXI2MouseButtonTransitions(t *testing.T) {
	state := newXI2State(ObservationFilter{Button: 1, Transition: "both"}, nil)
	for _, tc := range []struct {
		kind   uint16
		detail uint32
		want   bool
	}{
		{15, 2, false}, {15, 1, true}, {15, 1, false}, {16, 1, true}, {16, 1, false},
	} {
		ev, ok := state.onEvent(xi2Raw{kind: tc.kind, detail: tc.detail, source: 5})
		if ok != tc.want {
			t.Fatalf("%+v: %v", tc, ok)
		}
		if ok && (ev.Kind != "mouse-button" || ev.ID != "1" || ev.Source != "x11-xi2") {
			t.Fatal(ev)
		}
	}
	state = newXI2State(ObservationFilter{Button: 1, Transition: "press", ReportRepeat: true}, nil)
	_, _ = state.onEvent(xi2Raw{kind: 15, detail: 1, source: 7})
	repeat, ok := state.onEvent(xi2Raw{kind: 15, detail: 1, source: 7})
	if !ok || !repeat.Repeat {
		t.Fatal("requested repeat not reported")
	}
}
func TestXI2Keysyms(t *testing.T) {
	for token, want := range map[string]uint32{"F8": 0xffc5, "P": 'p', "0": '0'} {
		got, err := keysymForKey(token)
		if err != nil || got != want {
			t.Fatal(token, got, err)
		}
	}
	for _, token := range []string{"F0", "F13", "XF8", "?"} {
		if _, err := keysymForKey(token); err == nil {
			t.Fatalf("invalid %q accepted", token)
		}
	}
}
func TestObserverCloseRevokesOwnedSubscribers(t *testing.T) {
	observer := &x11Observer{subscriptions: make(map[*observationSubscription]struct{})}
	sub := newObservationSubscription(nil)
	observer.subscriptions[sub] = struct{}{}
	if err := observer.Close(); err != nil || !errors.Is(sub.Err(), ErrObservationRevoked) {
		t.Fatal(err, sub.Err())
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}

}

func TestXI2ConnectErrorClassification(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want error
	}{
		{nil, nil},
		{errors.New("x protocol authentication refused: Invalid MIT-MAGIC-COOKIE-1"), ErrObservationDenied},
		{errors.New("connection reset by peer"), ErrObservationUnavailable},
	} {
		got := classifyXI2ConnectionError(tt.err)
		if tt.want == nil {
			if got != nil {
				t.Fatal(got)
			}
		} else if !errors.Is(got, tt.want) {
			t.Fatal(got)
		}
	}
}

func TestObservationClosedTerminationAndSuperModifier(t *testing.T) {
	s := newObservationSubscription(nil)
	s.mu.Lock()
	s.terminateLocked(nil)
	s.terminateLocked(ErrObservationLoss)
	s.mu.Unlock()
	if s.Err() != nil {
		t.Fatal("closed subscription changed terminal error")
	}
	if _, err := normalizeObservationFilter(ObservationFilter{Combo: "Super+F8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeObservationFilter(ObservationFilter{Combo: "Meta+F8"}); err != nil {
		t.Fatal(err)
	}
}

func TestXI2StateRemainingTransitions(t *testing.T) {
	mapping := &xi2ChordMap{target: map[byte]bool{72: true}, modifiers: map[byte]string{37: "Ctrl", 50: "Shift"}, expected: map[string]bool{"Ctrl": true}}
	empty := newXI2State(ObservationFilter{Combo: "Ctrl+F8", Transition: "both"}, nil)
	if empty.chordActive() {
		t.Fatal("nil keymap matched")
	}
	if ev, ok := newXI2State(ObservationFilter{}, nil).onEvent(xi2Raw{kind: 42}); ok {
		t.Fatal("unknown transition accepted", ev)
	}
	state := newXI2State(ObservationFilter{Combo: "Ctrl+F8", Transition: "both"}, mapping)
	if state.hasDown() {
		t.Fatal("fresh state should be empty")
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 13, device: 3, detail: 37}); ok {
		t.Fatal(ev)
	}
	if !state.hasDown() {
		t.Fatal("failed to retain modifier state")
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 15, source: 3, detail: 1}); ok {
		t.Fatal("mouse button triggered hotkey", ev)
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 13, source: 3, detail: 50}); ok {
		t.Fatal("extra modifier triggered hotkey", ev)
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 13, source: 3, detail: 72}); ok {
		t.Fatal("extra modifier accepted", ev)
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 14, source: 3, detail: 50}); !ok || ev.Transition != "press" {
		t.Fatal("chord did not activate after extra modifier release", ev)
	}
	if ev, ok := state.onEvent(xi2Raw{kind: 14, source: 3, detail: 72}); !ok || ev.Transition != "release" {
		t.Fatal("release transition missing", ev)
	}
	filtered := newXI2State(ObservationFilter{Button: 1, Transition: "release"}, nil)
	if ev, ok := filtered.onEvent(xi2Raw{kind: 15, source: 1, detail: 1}); ok {
		t.Fatal("press passed release-only filter", ev)
	}
	if ev, ok := filtered.onEvent(xi2Raw{kind: 16, source: 1, detail: 1}); !ok || ev.Transition != "release" {
		t.Fatal("release filtering failed", ev)
	}
	fallback := newXI2State(ObservationFilter{Button: 1, Transition: "both"}, nil)
	if ev, ok := fallback.onEvent(xi2Raw{kind: 15, device: 2, detail: 1}); !ok {
		t.Fatal("fallback device event ignored", ev)
	}
}

func TestXI2StreamTerminationPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := observationStreamError(ctx, errors.New("socket closed")); !errors.Is(got, context.Canceled) {
		t.Fatal(got)
	}
	if got := observationStreamError(context.Background(), errors.New("wire fault")); !errors.Is(got, ErrObservationLoss) {
		t.Fatal(got)
	}
}

func TestXI2PostRegistrationAuthority(t *testing.T) {
	b := &x11Observer{subscriptions: map[*observationSubscription]struct{}{}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	sub := newObservationSubscription(nil)
	if err := b.publishSubscription(canceled, sub); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(b.subscriptions) != 0 {
		t.Fatal("published canceled subscription")
	}
	select {
	case <-sub.done:
	default:
		t.Fatal("canceled subscription not closed")
	}
	active := newObservationSubscription(nil)
	if err := b.publishSubscription(context.Background(), active); err != nil {
		t.Fatal(err)
	}
	if len(b.subscriptions) != 1 {
		t.Fatal("successful registration not published")
	}
	_ = b.Close()
	revoked := newObservationSubscription(nil)
	if err := b.publishSubscription(context.Background(), revoked); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
	if !errors.Is(revoked.Err(), ErrObservationRevoked) {
		t.Fatal(revoked.Err())
	}
}

func TestXI2DispatchRejectsMalformedSourceAndBoundedOverflow(t *testing.T) {
	mkState := func() *xi2State { return newXI2State(ObservationFilter{Button: 1, Transition: "both"}, nil) }
	malformed := newObservationSubscription(nil)
	if dispatchXI2Generic([]byte{35}, 131, mkState(), malformed, func(string) bool { return true }) {
		t.Fatal("malformed frame accepted")
	}
	if !errors.Is(malformed.Err(), ErrObservationLoss) {
		t.Fatal(malformed.Err())
	}
	sub := newObservationSubscription(nil)
	var refreshes int
	refresh := func(string) bool { refreshes++; return true }
	if !dispatchXI2Generic(rawXI2(13, 72, 132, 1, 1), 131, mkState(), sub, refresh) {
		t.Fatal("different extension terminated listener")
	}
	if !dispatchXI2Generic(rawXI2(11, 0, 131, 1, 1), 131, mkState(), sub, refresh) {
		t.Fatal("refresh terminated listener")
	}
	if refreshes != 1 {
		t.Fatalf("refresh calls %d", refreshes)
	}
	if dispatchXI2Generic(rawXI2(11, 0, 131, 1, 1), 131, mkState(), sub, func(string) bool { return false }) {
		t.Fatal("failed refresh accepted")
	}
	held := mkState()
	if !dispatchXI2Generic(rawXI2(15, 1, 131, 1, 1), 131, held, sub, refresh) {
		t.Fatal("button event dropped")
	}
	if got := <-sub.Events(); got.Transition != "press" {
		t.Fatal(got)
	}
	for i := 0; i < observationQueueSize; i++ {
		if !sub.push(ObservationEvent{Kind: "mouse-button"}) {
			t.Fatal("early overflow")
		}
	}
	if dispatchXI2Generic(rawXI2(16, 1, 131, 1, 1), 131, held, sub, refresh) {
		t.Fatal("overflow not terminal")
	}
	if !errors.Is(sub.Err(), ErrObservationLoss) {
		t.Fatal(sub.Err())
	}
}

func TestXI2XwaylandProtocolAuthority(t *testing.T) {
	for _, tc := range []struct {
		reply *xproto.QueryExtensionReply
		want  error
	}{
		{nil, ErrObservationUnavailable},
		{&xproto.QueryExtensionReply{Present: true}, ErrObservationUnsupported},
		{&xproto.QueryExtensionReply{Present: false}, nil},
	} {
		if err := validateNativeX11Extension(tc.reply); !errors.Is(err, tc.want) {
			t.Fatalf("reply %+v error %v want %v", tc.reply, err, tc.want)
		}
	}
}

func TestXI2ReadBoundaryLossAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		event xgb.Event
		want  error
		ok    bool
	}{
		{"error", errors.New("synthetic read failure"), nil, ErrObservationLoss, false},
		{"closed", nil, nil, ErrObservationLoss, false},
		{"event", nil, xproto.GeGenericEvent{}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := newObservationSubscription(nil)
			ev, ok := receiveXI2Event(context.Background(), sub, func() (xgb.Event, error) { return tc.event, tc.err })
			if ok != tc.ok || (ok && ev == nil) {
				t.Fatalf("read result: %T %v", ev, ok)
			}
			if !errors.Is(sub.Err(), tc.want) {
				t.Fatalf("error %v want %v", sub.Err(), tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sub := newObservationSubscription(nil)
	if _, ok := receiveXI2Event(ctx, sub, func() (xgb.Event, error) { return nil, errors.New("closed socket") }); ok {
		t.Fatal("canceled socket was accepted")
	}
	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatal(sub.Err())
	}
}

func TestX11ObservePointer(t *testing.T) {
	for _, same := range []bool{true, false} {
		mock := &x11.MockConnection{}
		mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
			return syntheticPointerReply{reply: &xproto.QueryPointerReply{SameScreen: same, RootX: -52, RootY: 17}}
		}
		b := &XTestBackend{conn: mock, root: 1}
		got, err := b.ObservePointer(context.Background())
		if !same {
			if !errors.Is(err, ErrNotSupported) || got != (PointerObservation{}) {
				t.Fatalf("different root: %+v %v", got, err)
			}
		} else if err != nil || got.X != -52 || got.Y != 17 || got.Provider != "x11-root" || got.ObservedAt.IsZero() || got.CoordinateSpace.Kind != PointerCoordinateSpaceRootPixels {
			t.Fatalf("got %+v %v", got, err)
		}
	}
}
func TestUnsupportedBackendsHaveNoPointerObserver(t *testing.T) {
	for _, b := range []any{&RemoteDesktopBackend{}, &WlVirtualBackend{}, &UinputBackend{}} {
		if _, ok := b.(PointerObserver); ok {
			t.Fatalf("%T unexpectedly observes pointer", b)
		}
	}
}

type observationGnomeBridge struct{ lifecycleGnomeBridge }

func (*observationGnomeBridge) PointerLocation(context.Context) (int, int, error) { return -14, 0, nil }
func TestGnomeObservePointer(t *testing.T) {
	b := &GnomeNativeBackend{bridge: &observationGnomeBridge{}}
	got, err := b.ObservePointer(context.Background())
	if err != nil || got.X != -14 || got.Y != 0 || got.Provider != "gnome-native" || got.ObservedAt.IsZero() || got.CoordinateSpace != (CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}) {
		t.Fatalf("got %+v %v", got, err)
	}
}

// Controlled test only: injected events establish XI2 transport delivery,
// not the provenance of real physical keyboard or mouse hardware.
// Enable with PF_X11_OBSERVATION_INTEGRATION=1; each test owns a private Xvfb.
// Each controlled integration owns a fresh Xvfb server with a confirmed
// display identity. Reusing a server after an unrelated test can reset
// connections on CI. Its process and display are cleaned up after the test.
func requireObservationX11TestServer(t *testing.T) string {
	t.Helper()
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires controlled native X11 integration")
	}
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatalf("start owned Xvfb: %v", err)
	}
	t.Cleanup(stop)
	// Xvfb's displayfd announces the socket before all extensions have
	// finished initializing. Give the isolated server a bounded settling
	// interval before the first XI2 version or keyboard-mapping request.
	time.Sleep(350 * time.Millisecond)
	t.Setenv("DISPLAY", display)
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("WAYLAND_SOCKET", "")
	return display
}

func TestObservationX11ControlledIntegration(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatalf("open native observer: %v", err)
	}
	defer backend.Close()
	// Use a preinitialized XTEST connection on this isolated Xvfb server.
	// No production input-observation path depends on XTEST.
	injector, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	if err := xtest.Init(injector); err != nil {
		t.Fatal(err)
	}
	keymap, err := loadXI2ChordMap(injector, "F8")
	if err != nil {
		t.Fatal(err)
	}
	var f8 byte
	for code := range keymap.target {
		f8 = code
		break
	}
	root := xproto.Setup(injector).DefaultScreen(injector).Root
	inject := func(kind byte, key byte) {
		t.Helper()
		if err := xtest.FakeInputChecked(injector, kind, key, xproto.TimeCurrentTime, root, 0, 0, 0).Check(); err != nil {
			t.Fatalf("inject controlled event: %v", err)
		}
	}
	for _, tt := range []struct {
		name    string
		filter  ObservationFilter
		press   byte
		release byte
		key     byte
	}{
		{"key", ObservationFilter{Combo: "F8", Transition: "both"}, xproto.KeyPress, xproto.KeyRelease, f8},
		{"button", ObservationFilter{Button: 1, Transition: "both"}, xproto.ButtonPress, xproto.ButtonRelease, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listening, err := backend.Listen(ctx, tt.filter)
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer listening.Close()
			inject(tt.press, tt.key)
			inject(tt.release, tt.key)
			var presses, releases int
			timer := time.NewTimer(time.Second * 2)
			defer timer.Stop()
			for presses == 0 || releases == 0 {
				select {
				case <-timer.C:
					t.Fatalf("missing synthetic events press=%d release=%d err=%v", presses, releases, listening.Err())
				case ev, ok := <-listening.Events():
					if !ok {
						t.Fatalf("listener closed prematurely: %v", listening.Err())
					}
					if ev.Source != "x11-xi2" {
						t.Fatalf("unexpected source: %+v", ev)
					}
					if ev.Transition == "press" {
						presses++
					}
					if ev.Transition == "release" {
						releases++
					}
				}
			}
			if err := listening.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case _, ok := <-listening.Events():
				if ok {
					t.Fatal("unexpected event after closure")
				}
			case <-time.After(time.Second):
				t.Fatal("listener did not terminate")
			}
		})
	}
	listening, err := backend.Listen(ctx, ObservationFilter{Button: 1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-listening.Events():
			if !ok {
				if !errors.Is(listening.Err(), context.Canceled) {
					t.Fatalf("cancellation error: %v", listening.Err())
				}
				return
			}
		case <-deadline:
			t.Fatal("cancellation failed to close listener")
		}
	}
}

// TestObservationX11CrossFocusIntegration proves that a global XI2 listener
// still sees the named hotkey while focus moves between different X windows.
// XTEST is used strictly for controlled CI input; it does not attest to
// physical keyboard provenance or grant broader backend permissions.
func TestObservationX11CrossFocusIntegration(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatalf("open native X11 observer: %v", err)
	}
	defer backend.Close()
	listening, err := backend.Listen(ctx, ObservationFilter{Combo: "F8", Transition: "both"})
	if err != nil {
		t.Fatalf("register global hotkey: %v", err)
	}
	defer listening.Close()

	client, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := xtest.Init(client); err != nil {
		t.Fatal(err)
	}
	keymap, err := loadXI2ChordMap(client, "F8")
	if err != nil {
		t.Fatal(err)
	}
	var keycode byte
	for code := range keymap.target {
		keycode = code
		break
	}
	if keycode == 0 {
		t.Fatal("native X11 keymap has no F8 key")
	}
	screen := xproto.Setup(client).DefaultScreen(client)
	var windows []xproto.Window
	defer func() {
		for _, window := range windows {
			_ = xproto.DestroyWindowChecked(client, window).Check()
		}
	}()
	for i := 0; i < 2; i++ {
		id, err := xproto.NewWindowId(client)
		if err != nil {
			t.Fatal(err)
		}
		if err := xproto.CreateWindowChecked(client, screen.RootDepth, id, screen.Root,
			int16(20+i*250), 20, 160, 120, 0, xproto.WindowClassInputOutput,
			screen.RootVisual, 0, nil).Check(); err != nil {
			t.Fatal(err)
		}
		windows = append(windows, id)
		if err := xproto.MapWindowChecked(client, id).Check(); err != nil {
			t.Fatal(err)
		}
	}
	for i, window := range windows {
		if err := xproto.SetInputFocusChecked(client, xproto.InputFocusNone, window, xproto.TimeCurrentTime).Check(); err != nil {
			t.Fatalf("focus window %d: %v", i, err)
		}
		actual, err := xproto.GetInputFocus(client).Reply()
		if err != nil || actual.Focus != window {
			t.Fatalf("focus window %d not acknowledged: focus=%v err=%v", i, actual, err)
		}
		for _, kind := range []byte{xproto.KeyPress, xproto.KeyRelease} {
			if err := xtest.FakeInputChecked(client, kind, keycode, xproto.TimeCurrentTime, screen.Root, 0, 0, 0).Check(); err != nil {
				t.Fatalf("XTEST key event while window %d focused: %v", i, err)
			}
			want := "press"
			if kind == xproto.KeyRelease {
				want = "release"
			}
			select {
			case ev, ok := <-listening.Events():
				if !ok {
					t.Fatalf("global listener ended: %v", listening.Err())
				}
				if ev.Source != "x11-xi2" || ev.Transition != want {
					t.Fatalf("focus %d: wrong global event: %+v, want %s", i, ev, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("global F8 %s missing while window %d focused", want, i)
			}
		}
	}
}

func TestObservationX11SessionClose(t *testing.T) {
	requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatal(err)
	}
	listening, err := backend.Listen(ctx, ObservationFilter{Combo: "F8"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-listening.Events():
		if ok {
			t.Fatal("event after close")
		}
		if !errors.Is(listening.Err(), ErrObservationRevoked) {
			t.Fatal(listening.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked")
	}
}

// OpenObserver's native-X11 gate must honor caller cancellation after a
// positively identified local X server, not only on unsupported Wayland.
func TestObservationX11CanceledAfterNativePeerVerification(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if backend, err := openX11Observer(ctx, display); backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native backend: %T %v", backend, err)
	}
}
