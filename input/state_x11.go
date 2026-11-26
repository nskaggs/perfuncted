package input

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
)

func (b *x11Observer) stateConnection(ctx context.Context) (*xgb.Conn, func(), error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("%w: nil context", ErrInvalidStateQuery)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return nil, nil, ErrObservationRevoked
	}
	if err := nativeX11Peer(b.display); err != nil {
		return nil, nil, err
	}
	conn, _, err := openXI2Connection(ctx, b.display)
	if err != nil {
		return nil, nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		conn.Close()
		return nil, nil, ctxErr
	}
	stopCancel := context.AfterFunc(ctx, conn.Abort)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		stopCancel()
		conn.Abort()
		return nil, nil, ErrObservationRevoked
	}
	if b.stateConns == nil {
		b.stateConns = make(map[*xgb.Conn]struct{})
	}
	b.stateConns[conn] = struct{}{}
	b.mu.Unlock()
	return conn, func() {
		gracefulClose := stopCancel() && ctx.Err() == nil
		if gracefulClose {
			conn.Close()
		} else {
			conn.Abort()
		}
		b.mu.Lock()
		delete(b.stateConns, conn)
		b.mu.Unlock()
	}, nil
}

func (b *x11Observer) stateQueryError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrObservationRevoked
	}
	if errors.Is(err, ErrObservationUnsupported) || errors.Is(err, ErrObservationUnavailable) || errors.Is(err, ErrObservationDenied) {
		return err
	}
	var access xproto.AccessError
	if errors.As(err, &access) {
		return fmt.Errorf("%w: X11 state query: %w", ErrObservationDenied, err)
	}
	return fmt.Errorf("%w: X11 state query: %w", ErrObservationUnavailable, err)
}

func (b *x11Observer) finishStateQuery(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	b.mu.Lock()
	closed := b.closed
	b.mu.Unlock()
	if closed {
		return ErrObservationRevoked
	}
	return nil
}

func (b *x11Observer) KeyState(ctx context.Context, name string) (StateObservation, error) {
	canonical, symbols, err := normalizeStateKey(name)
	if err != nil {
		return StateObservation{}, err
	}
	conn, closeConn, err := b.stateConnection(ctx)
	if err != nil {
		return StateObservation{}, err
	}
	defer closeConn()
	codes, err := x11KeycodesForSymbols(conn, symbols)
	if err != nil {
		return StateObservation{}, b.stateQueryError(ctx, err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return StateObservation{}, ctxErr
	}
	reply, err := xproto.QueryKeymap(conn).Reply()
	if err != nil {
		return StateObservation{}, b.stateQueryError(ctx, err)
	}
	if reply == nil {
		return StateObservation{}, ErrObservationUnavailable
	}
	if err := b.finishStateQuery(ctx); err != nil {
		return StateObservation{}, err
	}
	pressed := false
	for _, code := range codes {
		index := int(code) / 8
		if index >= len(reply.Keys) {
			return StateObservation{}, ErrObservationUnavailable
		}
		pressed = pressed || reply.Keys[index]&(1<<uint(code%8)) != 0
	}
	state := StateReleased
	if pressed {
		state = StatePressed
	}
	return StateObservation{
		Kind: "key", Name: canonical, State: state, Provider: "x11-core",
		Provenance: "unknown", ObservedAt: time.Now().UTC(),
	}, nil
}

func (b *x11Observer) LockState(ctx context.Context, name string) (StateObservation, error) {
	canonical, keysym, err := normalizeLockStateName(name)
	if err != nil {
		return StateObservation{}, err
	}
	conn, closeConn, err := b.stateConnection(ctx)
	if err != nil {
		return StateObservation{}, err
	}
	defer closeConn()
	mask, err := x11LockModifierMask(conn, canonical, keysym)
	if err != nil {
		return StateObservation{}, b.stateQueryError(ctx, err)
	}
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		return StateObservation{}, ErrObservationUnavailable
	}
	reply, err := xproto.QueryPointer(conn, setup.DefaultScreen(conn).Root).Reply()
	if err != nil {
		return StateObservation{}, b.stateQueryError(ctx, err)
	}
	if reply == nil {
		return StateObservation{}, ErrObservationUnavailable
	}
	if err := b.finishStateQuery(ctx); err != nil {
		return StateObservation{}, err
	}
	state := StateDisabled
	if reply.Mask&mask != 0 {
		state = StateEnabled
	}
	return StateObservation{
		Kind: "lock", Name: canonical, State: state, Provider: "x11-core",
		Provenance: "unknown", ObservedAt: time.Now().UTC(),
	}, nil
}

func x11LockModifierMask(conn *xgb.Conn, name string, keysym xproto.Keysym) (uint16, error) {
	requested, err := x11KeycodesForSymbols(conn, []xproto.Keysym{keysym})
	if err != nil {
		return 0, err
	}
	requestedSet := make(map[xproto.Keycode]bool, len(requested))
	for _, code := range requested {
		requestedSet[code] = true
	}
	otherLocks := make(map[xproto.Keycode]bool)
	for otherName, otherKeysym := range stateLockKeysyms {
		if otherName == name {
			continue
		}
		codes, mapErr := x11KeycodesForSymbols(conn, []xproto.Keysym{otherKeysym})
		if errors.Is(mapErr, ErrObservationUnsupported) {
			continue
		}
		if mapErr != nil {
			return 0, mapErr
		}
		for _, code := range codes {
			otherLocks[code] = true
		}
	}

	mapping, err := xproto.GetModifierMapping(conn).Reply()
	if err != nil {
		return 0, err
	}
	if mapping == nil || mapping.KeycodesPerModifier == 0 || len(mapping.Keycodes) != int(mapping.KeycodesPerModifier)*8 {
		return 0, ErrObservationUnavailable
	}
	return resolveX11LockModifierMask(mapping, requestedSet, otherLocks)
}

func resolveX11LockModifierMask(
	mapping *xproto.GetModifierMappingReply,
	requestedSet map[xproto.Keycode]bool,
	otherLocks map[xproto.Keycode]bool,
) (uint16, error) {
	if mapping == nil || mapping.KeycodesPerModifier == 0 || len(mapping.Keycodes) != int(mapping.KeycodesPerModifier)*8 {
		return 0, ErrObservationUnavailable
	}
	modifierMasks := [...]uint16{
		xproto.ModMaskShift, xproto.ModMaskLock, xproto.ModMaskControl,
		xproto.ModMask1, xproto.ModMask2, xproto.ModMask3,
		xproto.ModMask4, xproto.ModMask5,
	}
	selected, err := findX11LockModifier(mapping, requestedSet, modifierMasks[:])
	if err != nil {
		return 0, err
	}
	if selected == 0 {
		return 0, ErrObservationUnsupported
	}
	selectedIndex := 0
	for i, mask := range modifierMasks {
		if mask == selected {
			selectedIndex = i
			break
		}
	}
	start := selectedIndex * int(mapping.KeycodesPerModifier)
	for _, code := range mapping.Keycodes[start : start+int(mapping.KeycodesPerModifier)] {
		// QueryPointer reports only the aggregate modifier bit. If any other
		// keycode shares that modifier slot, its pressed state is
		// indistinguishable from the requested lock key and the result would
		// be a false lock-state claim.
		if code != 0 && (otherLocks[code] || !requestedSet[code]) {
			return 0, ErrObservationUnsupported
		}
	}
	return selected, nil
}

func (b *x11Observer) MouseButtonState(ctx context.Context, button int) (StateObservation, error) {
	if err := ValidateMouseButtonStateQuery(button); err != nil {
		return StateObservation{}, err
	}
	conn, closeConn, err := b.stateConnection(ctx)
	if err != nil {
		return StateObservation{}, err
	}
	defer closeConn()
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 || conn.DefaultScreen < 0 || conn.DefaultScreen >= len(setup.Roots) {
		return StateObservation{}, ErrObservationUnavailable
	}
	reply, err := xproto.QueryPointer(conn, setup.DefaultScreen(conn).Root).Reply()
	if err != nil {
		return StateObservation{}, b.stateQueryError(ctx, err)
	}
	if reply == nil {
		return StateObservation{}, ErrObservationUnavailable
	}
	if err := b.finishStateQuery(ctx); err != nil {
		return StateObservation{}, err
	}
	buttonMask := []uint16{0, xproto.KeyButMaskButton1, xproto.KeyButMaskButton2, xproto.KeyButMaskButton3, xproto.KeyButMaskButton4, xproto.KeyButMaskButton5}[button]
	state := StateReleased
	if reply.Mask&buttonMask != 0 {
		state = StatePressed
	}
	return StateObservation{
		Kind: "button", Name: fmt.Sprintf("%d", button), State: state,
		Provider: "x11-core", Provenance: "unknown", ObservedAt: time.Now().UTC(),
	}, nil
}

func x11KeycodesForSymbols(conn *xgb.Conn, symbols []xproto.Keysym) ([]xproto.Keycode, error) {
	setup := xproto.Setup(conn)
	if setup == nil || setup.MaxKeycode < setup.MinKeycode {
		return nil, ErrObservationUnavailable
	}
	count := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	if count <= 0 || count > 255 {
		return nil, ErrObservationUnavailable
	}
	reply, err := xproto.GetKeyboardMapping(conn, setup.MinKeycode, byte(count)).Reply()
	if err != nil {
		return nil, err
	}
	if reply == nil || reply.KeysymsPerKeycode == 0 {
		return nil, ErrObservationUnavailable
	}
	stride := int(reply.KeysymsPerKeycode)
	if stride > len(reply.Keysyms)/count || count*stride != len(reply.Keysyms) {
		return nil, ErrObservationUnavailable
	}
	wanted := make(map[xproto.Keysym]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	codes := make([]xproto.Keycode, 0, len(symbols))
	for offset, symbol := range reply.Keysyms {
		if wanted[symbol] {
			code := int(setup.MinKeycode) + offset/stride
			if code > int(setup.MaxKeycode) {
				return nil, ErrObservationUnavailable
			}
			codes = append(codes, xproto.Keycode(code))
		}
	}
	if len(codes) == 0 {
		return nil, ErrObservationUnsupported
	}
	return codes, nil
}

func findX11LockModifier(mapping *xproto.GetModifierMappingReply, requestedSet map[xproto.Keycode]bool, modifierMasks []uint16) (uint16, error) {
	var selected uint16
	for modifier, mask := range modifierMasks {
		start := modifier * int(mapping.KeycodesPerModifier)
		for _, code := range mapping.Keycodes[start : start+int(mapping.KeycodesPerModifier)] {
			if requestedSet[code] {
				if selected != 0 && selected != mask {
					return 0, ErrObservationUnsupported
				}
				selected = mask
			}
		}
	}
	return selected, nil
}
