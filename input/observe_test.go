package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/env"
)

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
