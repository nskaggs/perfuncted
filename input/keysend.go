// Package input provides keyboard and mouse injection backends.
//
// The Type method accepts a human-readable key syntax:
//
//   - Literal text is typed character-by-character (layout-independent).
//   - {keyname} sends a named key: {enter}, {tab}, {escape}, {f1}, {ctrl}, etc.
//   - {keyname down} holds a key down; {keyname up} releases it.
//   - {modifier+key} sends a key combination with named modifiers:
//     ctrl+, alt+, shift+, super+ (or meta+)
//   - {ctrl+shift+t}    → Ctrl+Shift+T
//   - {alt+shift+left}  → Alt+Shift+Left
//   - {ctrl down}v{ctrl up} — holds Ctrl, taps V, releases Ctrl
//   - Examples:
//     Type("hello world")          → types "hello world"
//     Type("{enter}")              → presses Enter
//     Type("{ctrl+s}")             → Ctrl+S
//     Type("{ctrl+shift+left}")    → Ctrl+Shift+Left
package input

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// keySend represents a parsed key-send action produced by parseKeySend.
type keySend struct {
	// text is literal text to type (no special keys).
	text string
	// key is a named key to press (e.g. "enter", "f1", "ctrl").
	// Mutually exclusive with text.
	key string
	// down is true for an explicit key-down action.
	// When both down and up are false, the action is a tap (press+release).
	down bool
	// up is true for an explicit key-up action.
	up bool
	// modifiers to apply to this key.
	modifiers modifiers
}

type modifiers struct {
	ctrl  bool
	alt   bool
	shift bool
	super bool
}

// Named keys have a bounded grammar. Very large braced expressions cannot
// be valid keys and must not cause unbounded work after scanning a closing brace.
const maxKeyExpressionBytes = 256

// parseKeySend parses a human-readable key string into a sequence of keySend
// actions. Each action is either literal text to type or a named key to
// press/release.
// Literal text is returned as elements with a .text field. Braced expressions
// {keyname}, {keyname down/up}, or {mod+key} are returned with the .key field.
func parseKeySend(input string) ([]keySend, error) {
	return parseKeySendContext(context.Background(), input)
}

func parseKeySendContext(ctx context.Context, input string) ([]keySend, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input == "" {
		return nil, nil
	}
	var sends []keySend
	for i := 0; i < len(input); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if input[i] == '{' {
			end, err := indexByteContext(ctx, input[i:], '}')
			if err != nil {
				return nil, err
			}
			if end == -1 {
				return nil, fmt.Errorf("input: unclosed brace at offset %d", i)
			}
			expr := input[i+1 : i+end]
			if len(expr) > maxKeyExpressionBytes {
				return nil, fmt.Errorf("input: key expression exceeds %d bytes", maxKeyExpressionBytes)
			}
			ks, err := parseBraced(expr)
			if err != nil {
				return nil, err
			}
			sends = append(sends, ks)
			i += end + 1
		} else {
			next, err := indexByteContext(ctx, input[i:], '{')
			if err != nil {
				return nil, err
			}
			if next == -1 {
				sends = append(sends, keySend{text: input[i:]})
				break
			}
			sends = append(sends, keySend{text: input[i : i+next]})
			i += next
		}
	}
	return sends, ctx.Err()
}

func indexByteContext(ctx context.Context, text string, needle byte) (int, error) {
	const chunk = 32 * 1024
	for offset := 0; offset < len(text); offset += chunk {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		end := min(offset+chunk, len(text))
		if i := strings.IndexByte(text[offset:end], needle); i >= 0 {
			return offset + i, nil
		}
	}
	return -1, ctx.Err()
}

func parseBraced(expr string) (keySend, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return keySend{}, fmt.Errorf("empty key name in braces")
	}
	lower := strings.ToLower(expr)

	// Check for {keyname down} / {keyname up}
	down := false
	up := false
	name := lower
	if strings.HasSuffix(lower, " down") {
		name = strings.TrimSuffix(lower, " down")
		name = strings.TrimSpace(name)
		down = true
	} else if strings.HasSuffix(lower, " up") {
		name = strings.TrimSuffix(lower, " up")
		name = strings.TrimSpace(name)
		up = true
	}

	if name == "" {
		return keySend{}, fmt.Errorf("empty key name in braces")
	}

	// Check for modifier+key syntax: ctrl+s, ctrl+shift+t, alt+f4, etc.
	if strings.Contains(name, "+") {
		return parseCombo(name, down, up)
	}

	return keySend{
		key:  name,
		down: down,
		up:   up,
	}, nil
}

// parseCombo parses a braced expression containing "+" separators like
// "ctrl+s", "ctrl+shift+t", "alt+f4", "shift+left".
func parseCombo(name string, down, up bool) (keySend, error) {
	if !strings.Contains(name, "+") {
		return keySend{}, fmt.Errorf("invalid key combo %q", name)
	}
	var mod modifiers
	start := 0
	for {
		separator := strings.IndexByte(name[start:], '+')
		if separator == -1 {
			key := strings.TrimSpace(name[start:])
			if key == "" {
				return keySend{}, fmt.Errorf("empty key in combo %q", name)
			}
			if isModifierName(key) {
				return keySend{}, fmt.Errorf("combo %q ends with a modifier; add a non-modifier key", name)
			}
			return keySend{key: key, down: down, up: up, modifiers: mod}, nil
		}
		p := strings.TrimSpace(name[start : start+separator])
		switch p {
		case "ctrl", "control":
			mod.ctrl = true
		case "alt":
			mod.alt = true
		case "shift":
			mod.shift = true
		case "super", "meta", "win", "logo":
			mod.super = true
		default:
			return keySend{}, fmt.Errorf("unknown modifier %q in combo %q", p, name)
		}
		start += separator + 1
	}
}

func isModifierName(s string) bool {
	switch s {
	case "ctrl", "control", "alt", "shift", "super", "meta", "win", "logo":
		return true
	}
	return false
}

// ValidateKeySequence checks the syntax accepted by Type without dispatching
// input. It honors ctx and does not check backend support for named keys.
func ValidateKeySequence(ctx context.Context, sequence string) error {
	if ctx == nil {
		return errors.New("input: nil context")
	}
	if _, err := parseKeySendContext(ctx, sequence); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("input: invalid key sequence")
	}
	return nil
}
