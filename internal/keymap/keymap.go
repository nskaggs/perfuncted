package keymap

import "strings"

// Key is a generic key identifier shared by input backends.
type Key int

const (
	KeyUnknown Key = iota
	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ
	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9

	KeySpace
	KeyEnter
	KeyTab
	KeyBackspace
	KeyEscape
	KeyCtrl
	KeyAlt
	KeyShift
	KeySuper
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyInsert
	KeyDelete
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

var nameToKey = map[string]Key{
	// letters
	"a": KeyA, "b": KeyB, "c": KeyC, "d": KeyD, "e": KeyE, "f": KeyF,
	"g": KeyG, "h": KeyH, "i": KeyI, "j": KeyJ, "k": KeyK, "l": KeyL,
	"m": KeyM, "n": KeyN, "o": KeyO, "p": KeyP, "q": KeyQ, "r": KeyR,
	"s": KeyS, "t": KeyT, "u": KeyU, "v": KeyV, "w": KeyW, "x": KeyX,
	"y": KeyY, "z": KeyZ,

	// digits
	"0": Key0, "1": Key1, "2": Key2, "3": Key3, "4": Key4,
	"5": Key5, "6": Key6, "7": Key7, "8": Key8, "9": Key9,

	// named
	"space": KeySpace,
	"enter": KeyEnter, "return": KeyEnter,
	"tab":       KeyTab,
	"backspace": KeyBackspace,
	"escape":    KeyEscape, "esc": KeyEscape,

	// modifiers
	"ctrl": KeyCtrl, "control": KeyCtrl, "control_l": KeyCtrl,
	"alt": KeyAlt, "alt_l": KeyAlt,
	"shift": KeyShift, "shift_l": KeyShift,
	"super": KeySuper, "meta": KeySuper, "logo": KeySuper, "super_l": KeySuper,

	// arrows and navigation
	"up": KeyUp, "down": KeyDown, "left": KeyLeft, "right": KeyRight,
	"home": KeyHome, "end": KeyEnd,
	"page_up": KeyPageUp, "pageup": KeyPageUp, "prior": KeyPageUp,
	"page_down": KeyPageDown, "pagedown": KeyPageDown, "next": KeyPageDown,
	"insert": KeyInsert, "delete": KeyDelete,

	// function keys
	"f1": KeyF1, "f2": KeyF2, "f3": KeyF3, "f4": KeyF4, "f5": KeyF5, "f6": KeyF6,
	"f7": KeyF7, "f8": KeyF8, "f9": KeyF9, "f10": KeyF10, "f11": KeyF11, "f12": KeyF12,
}

// FromString maps a canonical key name to Key.
func FromString(s string) (Key, bool) {
	k, ok := nameToKey[strings.ToLower(s)]
	return k, ok
}

// IsModifier reports whether k is a modifier key.
func IsModifier(k Key) bool {
	switch k {
	case KeyCtrl, KeyAlt, KeyShift, KeySuper:
		return true
	}
	return false
}
