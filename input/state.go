package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb/xproto"
)

// ErrInvalidStateQuery reports a malformed or unsupported state selector.
var ErrInvalidStateQuery = errors.New("input state: invalid query")

// StateValue reports the observed state of a key, lock or button.
type StateValue string

const (
	// StatePressed means the key or button is currently held.
	StatePressed StateValue = "pressed"
	// StateReleased means the key or button is currently not held.
	StateReleased StateValue = "released"
	// StateEnabled means the named lock mode is active.
	StateEnabled StateValue = "enabled"
	// StateDisabled means the named lock mode is inactive.
	StateDisabled StateValue = "disabled"
	// StateUnknown means the provider cannot determine the current state.
	StateUnknown StateValue = "unknown"
)

// StateObservation is one fresh, on-demand X11 state query. X11 core state is
// logical aggregate state and may include XTest-generated events, so this API
// never claims that the state came from a physical human-operated device.
type StateObservation struct {
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	State      StateValue `json:"state"`
	Provider   string     `json:"provider"`
	Provenance string     `json:"provenance"`
	ObservedAt time.Time  `json:"observed_at"`
}

// StateReader is an optional on-demand state surface. Inputter remains an
// injection-only interface; backends that cannot observe state do not
// implement StateReader.
type StateReader interface {
	KeyState(context.Context, string) (StateObservation, error)
	LockState(context.Context, string) (StateObservation, error)
	MouseButtonState(context.Context, int) (StateObservation, error)
}

var stateKeysyms = map[string][]xproto.Keysym{
	"shift":       {0xffe1, 0xffe2},
	"ctrl":        {0xffe3, 0xffe4},
	"control":     {0xffe3, 0xffe4},
	"alt":         {0xffe9, 0xffea},
	"super":       {0xffeb, 0xffec},
	"meta":        {0xffeb, 0xffec},
	"win":         {0xffeb, 0xffec},
	"shift_l":     {0xffe1},
	"leftshift":   {0xffe1},
	"shift_r":     {0xffe2},
	"rightshift":  {0xffe2},
	"ctrl_l":      {0xffe3},
	"control_l":   {0xffe3},
	"leftctrl":    {0xffe3},
	"ctrl_r":      {0xffe4},
	"control_r":   {0xffe4},
	"rightctrl":   {0xffe4},
	"alt_l":       {0xffe9},
	"leftalt":     {0xffe9},
	"alt_r":       {0xffea},
	"rightalt":    {0xffea},
	"super_l":     {0xffeb},
	"leftsuper":   {0xffeb},
	"super_r":     {0xffec},
	"rightsuper":  {0xffec},
	"capslock":    {0xffe5},
	"caps_lock":   {0xffe5},
	"numlock":     {0xff7f},
	"num_lock":    {0xff7f},
	"scrolllock":  {0xff14},
	"scroll_lock": {0xff14},
}

var stateLockKeysyms = map[string]xproto.Keysym{
	"capslock":   0xffe5,
	"numlock":    0xff7f,
	"scrolllock": 0xff14,
}

func normalizeStateKey(name string) (string, []xproto.Keysym, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || strings.ContainsRune(name, '\x00') {
		return "", nil, fmt.Errorf("%w: key name must be non-empty", ErrInvalidStateQuery)
	}
	if symbols, ok := stateKeysyms[name]; ok {
		return name, symbols, nil
	}
	if symbol, ok := keysymForName[name]; ok {
		return name, []xproto.Keysym{symbol}, nil
	}
	return "", nil, fmt.Errorf("%w: unsupported key name %q", ErrInvalidStateQuery, name)
}

// ValidateKeyStateQuery rejects malformed names without opening an observer.
func ValidateKeyStateQuery(name string) error {
	_, _, err := normalizeStateKey(name)
	return err
}

func normalizeLockStateName(name string) (string, xproto.Keysym, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "")
	symbol, ok := stateLockKeysyms[name]
	if !ok {
		return "", 0, fmt.Errorf("%w: unsupported lock state %q", ErrInvalidStateQuery, name)
	}
	return name, symbol, nil
}

// ValidateLockStateQuery rejects unsupported lock names before opening an observer.
func ValidateLockStateQuery(name string) error {
	_, _, err := normalizeLockStateName(name)
	return err
}

// ValidateMouseButtonStateQuery accepts core X11 buttons with defined masks.
func ValidateMouseButtonStateQuery(button int) error {
	if button < 1 || button > 5 {
		return fmt.Errorf("%w: X11 mouse button must be in [1,5]", ErrInvalidStateQuery)
	}
	return nil
}
