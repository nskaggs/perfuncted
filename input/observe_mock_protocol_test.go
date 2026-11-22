package input

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
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
	if ops := backend.SupportedOperations(); len(ops) != 2 {
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
