package input

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type x11Observer struct {
	display       string
	mu            sync.Mutex
	closed        bool
	subscriptions map[*observationSubscription]struct{}
}

func openX11Observer(ctx context.Context, display string) (*x11Observer, error) {
	if err := nativeX11Peer(display); err != nil {
		return nil, err
	}
	conn, opcode, err := openXI2Connection(ctx, display)
	if err != nil {
		return nil, err
	}
	_ = opcode
	conn.Abort()
	return &x11Observer{display: display, subscriptions: make(map[*observationSubscription]struct{})}, nil
}
func (b *x11Observer) SupportedOperations() []string {
	return []string{"listen-hotkey", "listen-button"}
}
func (b *x11Observer) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	subs := make([]*observationSubscription, 0, len(b.subscriptions))
	for s := range b.subscriptions {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		s.finish(ErrObservationRevoked)
	}
	return nil
}

//nolint:gocyclo // stateful X11 extension negotiation includes independent failure boundaries
func openXI2Connection(ctx context.Context, display string) (*xgb.Conn, byte, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, 0, ctxErr
	}
	c, err := xgb.NewConnDisplayContext(ctx, display)
	if err != nil {
		return nil, 0, classifyXI2ConnectionError(err)
	}
	fail := func(err error) (*xgb.Conn, byte, error) {
		c.Abort()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		return nil, 0, err
	}
	// XWAYLAND is an explicit protocol identity extension. The peer-process
	// check above additionally rejects older Xwayland lacking this extension.
	wayland, err := xproto.QueryExtension(c, uint16(len("XWAYLAND")), "XWAYLAND").Reply()
	if err != nil {
		return fail(fmt.Errorf("%w: XWAYLAND verification: %w", ErrObservationUnavailable, err))
	}
	if extensionErr := validateNativeX11Extension(wayland); extensionErr != nil {
		return fail(extensionErr)
	}
	extension, err := xproto.QueryExtension(c, uint16(len("XInputExtension")), "XInputExtension").Reply()
	if err != nil {
		return fail(fmt.Errorf("%w: XI2 query: %w", ErrObservationUnavailable, err))
	}
	if extension == nil || !extension.Present {
		return fail(ErrObservationUnsupported)
	}
	opcode := extension.MajorOpcode
	query := make([]byte, 8)
	query[0], query[1] = opcode, 47
	xgb.Put16(query[2:4], 2)
	xgb.Put16(query[4:6], 2)
	xgb.Put16(query[6:8], 1)
	ck := c.NewCookie(true, true)
	c.NewRequest(query, ck)
	reply, err := ck.Reply()
	if err != nil {
		return fail(fmt.Errorf("%w: XI2 version request: %w", ErrObservationUnavailable, err))
	}
	if len(reply) < 12 || xgb.Get16(reply[8:10]) < 2 {
		return fail(ErrObservationUnsupported)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fail(ctxErr)
	}
	return c, opcode, nil
}

// validateNativeX11Extension rejects XWayland when attested by the X server
// and treats a missing protocol response as unavailable, never as Xorg proof.
func validateNativeX11Extension(reply *xproto.QueryExtensionReply) error {
	if reply == nil {
		return ErrObservationUnavailable
	}
	if reply.Present {
		return ErrObservationUnsupported
	}
	return nil
}

// Only an explicit X11 protocol authentication refusal is reported as
// permission-denied; connectivity and ambiguous server failures are unavailable.
func classifyXI2ConnectionError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "x protocol authentication refused") {
		return fmt.Errorf("%w: x11 authentication: %w", ErrObservationDenied, err)
	}
	return fmt.Errorf("%w: x11 connection: %w", ErrObservationUnavailable, err)
}

// selectXI2 requests four raw transitions and state-change notification on
// this connection only. It never installs a keyboard or pointer grab.
func selectXI2(c *xgb.Conn, opcode byte, root xproto.Window, mask uint32) error {
	req := make([]byte, 20)
	req[0], req[1] = opcode, 46
	xgb.Put16(req[2:4], 5)
	xgb.Put32(req[4:8], uint32(root))
	xgb.Put16(req[8:10], 1)
	xgb.Put16(req[12:14], 0) // XIAllDevices; not an exclusive grab
	xgb.Put16(req[14:16], 1)
	xgb.Put32(req[16:20], mask)
	ck := c.NewCookie(true, false)
	c.NewRequest(req, ck)
	if err := ck.Check(); err != nil {
		var denied xproto.AccessError
		if errors.As(err, &denied) {
			return fmt.Errorf("%w: XISelectEvents: %w", ErrObservationDenied, err)
		}
		return fmt.Errorf("%w: XISelectEvents: %w", ErrObservationUnavailable, err)
	}
	return nil
}

//nolint:gocyclo // registration and cancellation must share an ownership boundary
func (b *x11Observer) Listen(ctx context.Context, filter ObservationFilter) (ObservationSubscription, error) {
	normalized, err := normalizeObservationFilter(filter)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidObservationFilter)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrObservationRevoked
	}
	b.mu.Unlock()
	conn, opcode, err := openXI2Connection(ctx, b.display)
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(conn)
	if len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		conn.Abort()
		return nil, ErrObservationUnavailable
	}
	root := setup.DefaultScreen(conn).Root
	mask := uint32((1 << 1) | (1 << 11) | (1 << 13) | (1 << 14) | (1 << 15) | (1 << 16))
	// A keyboard mapping must be resolved before registering a chord.
	var keymap *xi2ChordMap
	if normalized.Combo != "" {
		keymap, err = loadXI2ChordMap(conn, normalized.Combo)
		if err != nil {
			conn.Abort()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, err
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		conn.Abort()
		return nil, ctxErr
	}
	if selectErr := selectXI2(conn, opcode, root, mask); selectErr != nil {
		conn.Abort()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, selectErr
	}
	sub := newObservationSubscription(nil)
	sub.closeFn = func() {
		conn.Abort()
		b.mu.Lock()
		delete(b.subscriptions, sub)
		b.mu.Unlock()
	}
	if err := b.publishSubscription(ctx, sub); err != nil {
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			sub.finish(ctx.Err())
		case <-sub.done:
		}
	}()
	go func() {
		state := newXI2State(normalized, keymap)
		// A topology change when nothing is held can be handled by
		// refreshing the map; if a chord/button is already in progress,
		// transition history is uncertain and we fail terminally.
		refresh := func(reason string) bool {
			if state.hasDown() {
				sub.finish(fmt.Errorf("%w: %s with held input", ErrObservationLoss, reason))
				return false
			}
			if normalized.Combo != "" {
				newMap, mapErr := loadXI2ChordMap(conn, normalized.Combo)
				if mapErr != nil {
					sub.finish(errors.Join(ErrObservationLoss, mapErr))
					return false
				}
				state.keymap = newMap
			}
			return true
		}
		for {
			event, ok := receiveXI2Event(ctx, sub, func() (xgb.Event, error) {
				observed, protocolErr := conn.WaitForEvent()
				return observed, protocolErr
			})
			if !ok {
				return
			}
			if _, changed := event.(xproto.MappingNotifyEvent); changed {
				if !refresh("X11 keyboard mapping changed") {
					return
				}
				continue
			}
			generic, ok := event.(xproto.GeGenericEvent)
			if !ok {
				continue
			}
			if !dispatchXI2Generic(generic.Raw, opcode, state, sub, refresh) {
				return
			}
		}

	}()
	return sub, nil
}

// observationStreamError keeps authoritative caller cancellation distinct
// from genuine protocol stream loss, even when the socket read wins the race.
// receiveXI2Event is the sole XGB read boundary for this subscription.
// A valid event is returned; any transport/EOF condition terminates the
// stream. A callable read seam allows exact malformed-read regression tests.
func receiveXI2Event(ctx context.Context, sub *observationSubscription, next func() (xgb.Event, error)) (xgb.Event, bool) {
	event, protocolErr := next()
	if protocolErr != nil {
		sub.finish(observationStreamError(ctx, fmt.Errorf("XI2 stream: %w", protocolErr)))
		return nil, false
	}
	if event == nil {
		sub.finish(observationStreamError(ctx, errors.New("X11 connection closed")))
		return nil, false
	}
	return event, true
}

func observationStreamError(ctx context.Context, cause error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return errors.Join(ErrObservationLoss, cause)
}

// dispatchXI2Generic validates the captured frame before it affects chord
// state or reaches a consumer. Tests can supply malformed frames directly.
func dispatchXI2Generic(raw []byte, opcode byte, state *xi2State, sub *observationSubscription, refresh func(string) bool) bool {
	ev, recognized, decodeErr := decodeXI2Raw(raw, opcode)
	if decodeErr != nil {
		sub.finish(errors.Join(ErrObservationLoss, decodeErr))
		return false
	}
	if !recognized {
		return true
	}
	if ev.kind == 1 || ev.kind == 11 {
		return refresh("XI2 device hierarchy or mapping changed")
	}
	if outgoing, deliver := state.onEvent(ev); deliver {
		return sub.push(outgoing)
	}
	return true
}

// publishSubscription is the single post-registration authority check. Tests
// can deterministically exercise cancellation and concurrent Session.Close.
func (b *x11Observer) publishSubscription(ctx context.Context, sub *observationSubscription) error {
	b.mu.Lock()
	if b.closed || ctx.Err() != nil {
		b.mu.Unlock()
		sub.finish(ErrObservationRevoked)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrObservationRevoked
	}
	b.subscriptions[sub] = struct{}{}
	b.mu.Unlock()
	return nil
}

type xi2Raw struct {
	kind   uint16
	device uint16
	source uint16
	detail uint32
	flags  uint32
}

func decodeXI2Raw(buf []byte, opcode byte) (xi2Raw, bool, error) {
	if len(buf) < 32 {
		return xi2Raw{}, false, fmt.Errorf("short GenericEvent")
	}
	if buf[0]&127 != 35 || buf[1] != opcode {
		return xi2Raw{}, false, nil
	}
	expected := uint64(32) + uint64(xgb.Get32(buf[4:8]))*4
	if expected != uint64(len(buf)) {
		return xi2Raw{}, false, fmt.Errorf("malformed XI2 event length")
	}
	kind := xgb.Get16(buf[8:10])
	if kind != 13 && kind != 14 && kind != 15 && kind != 16 && kind != 1 && kind != 11 {
		return xi2Raw{}, false, nil
	}
	return xi2Raw{kind: kind, device: xgb.Get16(buf[10:12]), source: xgb.Get16(buf[20:22]), detail: xgb.Get32(buf[16:20]), flags: xgb.Get32(buf[24:28])}, true, nil
}

type xi2ChordMap struct {
	target    map[byte]bool
	modifiers map[byte]string
	expected  map[string]bool
}

func keysymForKey(token string) (uint32, error) {
	if len(token) == 1 {
		ch := token[0]
		if ch >= 'A' && ch <= 'Z' {
			return uint32(ch + ('a' - 'A')), nil
		}
		if ch >= '0' && ch <= '9' {
			return uint32(ch), nil
		}
	}
	if strings.HasPrefix(token, "F") {
		n, err := strconv.Atoi(token[1:])
		if err == nil && n >= 1 && n <= 12 {
			return 0xffbe + uint32(n-1), nil
		}
	}
	return 0, ErrInvalidObservationFilter
}

//nolint:gocyclo // validates full keycode/modifier mapping before admission
func loadXI2ChordMap(c *xgb.Conn, combo string) (*xi2ChordMap, error) {
	setup := xproto.Setup(c)
	first, last := setup.MinKeycode, setup.MaxKeycode
	if last < first {
		return nil, ErrObservationUnsupported
	}
	reply, err := xproto.GetKeyboardMapping(c, first, byte(int(last)-int(first)+1)).Reply()
	if err != nil {
		return nil, fmt.Errorf("%w: keyboard mapping: %w", ErrObservationUnavailable, err)
	}
	return chordMapFromKeyboardReply(first, last, reply, combo)
}

// chordMapFromKeyboardReply validates a complete, authoritative keymap before
// it is ever used for global chord transitions. Tests supply malformed replies
// without requiring a malformed X server or privileged input device.
func chordMapFromKeyboardReply(first, last xproto.Keycode, reply *xproto.GetKeyboardMappingReply, combo string) (*xi2ChordMap, error) {
	if reply == nil || reply.KeysymsPerKeycode == 0 {
		return nil, ErrObservationUnavailable
	}
	parts := strings.Split(combo, "+")
	key, err := keysymForKey(parts[len(parts)-1])
	if err != nil {
		return nil, err
	}
	target := map[byte]bool{}
	modifiers := map[byte]string{}
	modifierSyms := map[uint32]string{
		0xffe1: "Shift", 0xffe2: "Shift",
		0xffe3: "Ctrl", 0xffe4: "Ctrl",
		0xffe9: "Alt", 0xffea: "Alt",
		0xffeb: "Super", 0xffec: "Super",
	}
	stride := int(reply.KeysymsPerKeycode)
	for code := int(first); code <= int(last); code++ {
		off := (code - int(first)) * stride
		if off+stride > len(reply.Keysyms) {
			return nil, ErrObservationUnavailable
		}
		for _, sym := range reply.Keysyms[off : off+stride] {
			if uint32(sym) == key {
				target[byte(code)] = true
			}
			if category, ok := modifierSyms[uint32(sym)]; ok {
				modifiers[byte(code)] = category
			}
		}
	}
	if len(target) == 0 {
		return nil, fmt.Errorf("%w: requested key is absent from X11 keymap", ErrObservationUnsupported)
	}
	expected := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		expected[part] = true
	}
	for needed := range expected {
		found := false
		for _, actual := range modifiers {
			if needed == actual {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: modifier %s absent from X11 keymap", ErrObservationUnsupported, needed)
		}
	}
	return &xi2ChordMap{target: target, modifiers: modifiers, expected: expected}, nil
}

type xi2State struct {
	filter ObservationFilter
	keymap *xi2ChordMap
	down   map[uint16]map[uint32]bool
	active bool
}

func newXI2State(f ObservationFilter, k *xi2ChordMap) *xi2State {
	return &xi2State{filter: f, keymap: k, down: make(map[uint16]map[uint32]bool)}
}

// hasDown reports whether any event-observed key/button is held. It never
// infers state from the OS before registration.
func (s *xi2State) hasDown() bool {
	for _, held := range s.down {
		if len(held) > 0 {
			return true
		}
	}
	return false
}

func (s *xi2State) chordActive() bool {
	if s.keymap == nil {
		return false
	}
	// Do not invent a chord by combining Ctrl from one keyboard and
	// the target key from a different keyboard. A chord must be satisfied
	// by a single XI2 source device.
	for _, keys := range s.down {
		target := false
		mods := map[string]bool{}
		for code := range keys {
			if s.keymap.target[byte(code)] {
				target = true
			}
			if mod, ok := s.keymap.modifiers[byte(code)]; ok {
				mods[mod] = true
			}
		}
		if !target || len(mods) != len(s.keymap.expected) {
			continue
		}
		all := true
		for expected := range s.keymap.expected {
			if !mods[expected] {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

//nolint:gocyclo // preserves exact press/release and repeat transitions
func (s *xi2State) onEvent(e xi2Raw) (ObservationEvent, bool) {
	isKey := e.kind == 13 || e.kind == 14
	isButton := e.kind == 15 || e.kind == 16
	if !isKey && !isButton {
		return ObservationEvent{}, false
	}
	pressed := e.kind == 13 || e.kind == 15
	device := e.source
	if device == 0 {
		device = e.device
	}
	if s.down[device] == nil {
		s.down[device] = make(map[uint32]bool)
	}
	if s.filter.Button != 0 {
		if !isButton || int(e.detail) != s.filter.Button {
			return ObservationEvent{}, false
		}
	} else if !isKey {
		return ObservationEvent{}, false
	}
	down := s.down[device][e.detail]
	// XI2's KeyRepeat flag identifies a repeat without treating it as
	// a fresh physical edge. An unseen repeat at subscription start does
	// not establish the held-key state.
	flaggedRepeat := isKey && pressed && e.flags&(1<<16) != 0
	if flaggedRepeat && !down {
		return ObservationEvent{}, false
	}
	if pressed {
		if !down {
			s.down[device][e.detail] = true
		}
	} else {
		if !down {
			return ObservationEvent{}, false
		}
		delete(s.down[device], e.detail)
	}
	transition := "press"
	if !pressed {
		transition = "release"
	}
	repeated := pressed && (down || flaggedRepeat)
	if s.filter.Combo != "" {
		current := s.chordActive()
		switch {
		case !current && s.active:
			transition = "release"
		case current && !s.active:
			transition = "press"
		default:
			if !repeated || !current || !s.filter.ReportRepeat {
				return ObservationEvent{}, false
			}
		}
		s.active = current
	} else if repeated && !s.filter.ReportRepeat {
		return ObservationEvent{}, false
	}
	if s.filter.Transition != "both" && s.filter.Transition != transition {
		return ObservationEvent{}, false
	}
	id := s.filter.Combo
	kind := "hotkey"
	if s.filter.Button != 0 {
		id = fmt.Sprintf("%d", s.filter.Button)
		kind = "mouse-button"
	}
	return ObservationEvent{Kind: kind, ID: id, Transition: transition, Time: time.Now(), Source: "x11-xi2", Repeat: repeated}, true
}
