package keymap

import (
	"testing"
)

func TestFromString_Letters(t *testing.T) {
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		key, ok := FromString(string(c))
		if !ok {
			t.Errorf("FromString(%q) not found", c)
		}
		if key < KeyA || key > KeyZ {
			t.Errorf("FromString(%q) = %d, want KeyA–KeyZ range", c, key)
		}
	}
}

func TestFromString_Digits(t *testing.T) {
	for _, c := range "0123456789" {
		key, ok := FromString(string(c))
		if !ok {
			t.Errorf("FromString(%q) not found", c)
		}
		if key < Key0 || key > Key9 {
			t.Errorf("FromString(%q) = %d, want Key0–Key9 range", c, key)
		}
	}
}

func TestFromString_NamedKeys(t *testing.T) {
	tests := []struct {
		name string
		want Key
	}{
		{"space", KeySpace},
		{"enter", KeyEnter},
		{"tab", KeyTab},
		{"backspace", KeyBackspace},
		{"escape", KeyEscape},
		{"ctrl", KeyCtrl},
		{"alt", KeyAlt},
		{"shift", KeyShift},
		{"super", KeySuper},
		{"up", KeyUp},
		{"down", KeyDown},
		{"left", KeyLeft},
		{"right", KeyRight},
		{"home", KeyHome},
		{"end", KeyEnd},
		{"page_up", KeyPageUp},
		{"page_down", KeyPageDown},
		{"insert", KeyInsert},
		{"delete", KeyDelete},
		{"f1", KeyF1},
		{"f12", KeyF12},
	}
	for _, tc := range tests {
		got, ok := FromString(tc.name)
		if !ok {
			t.Errorf("FromString(%q) not found", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("FromString(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestFromString_Aliases(t *testing.T) {
	tests := []struct {
		alias string
		want  Key
	}{
		{"return", KeyEnter},
		{"esc", KeyEscape},
		{"control", KeyCtrl},
		{"control_l", KeyCtrl},
		{"alt_l", KeyAlt},
		{"shift_l", KeyShift},
		{"meta", KeySuper},
		{"logo", KeySuper},
		{"super_l", KeySuper},
		{"pageup", KeyPageUp},
		{"prior", KeyPageUp},
		{"pagedown", KeyPageDown},
		{"next", KeyPageDown},
	}
	for _, tc := range tests {
		got, ok := FromString(tc.alias)
		if !ok {
			t.Errorf("FromString(%q) alias not found", tc.alias)
			continue
		}
		if got != tc.want {
			t.Errorf("FromString(%q) = %d, want %d", tc.alias, got, tc.want)
		}
	}
}

func TestFromString_CaseInsensitive(t *testing.T) {
	tests := []string{"ENTER", "Enter", "eNtEr", "CTRL", "Escape", "TAB"}
	for _, name := range tests {
		if _, ok := FromString(name); !ok {
			t.Errorf("FromString(%q) should match (case-insensitive)", name)
		}
	}
}

func TestFromString_Unknown(t *testing.T) {
	unknowns := []string{"bogus", "", "f13", "µ", "ctrl+shift"}
	for _, name := range unknowns {
		if key, ok := FromString(name); ok {
			t.Errorf("FromString(%q) = %d, expected not-found", name, key)
		}
	}
}

func TestIsModifier(t *testing.T) {
	mods := []Key{KeyCtrl, KeyAlt, KeyShift, KeySuper}
	for _, k := range mods {
		if !IsModifier(k) {
			t.Errorf("IsModifier(%d) = false, want true", k)
		}
	}

	nonMods := []Key{KeyA, KeyEnter, KeySpace, KeyF1, Key0, KeyUp}
	for _, k := range nonMods {
		if IsModifier(k) {
			t.Errorf("IsModifier(%d) = true, want false", k)
		}
	}
}

func TestFromString_FKeys(t *testing.T) {
	for i := 1; i <= 12; i++ {
		name := "f" + string(rune('0'+i%10)) // f1..f9
		if i >= 10 {
			name = "f" + string(rune('0'+i/10)) + string(rune('0'+i%10)) // f10,f11,f12
		}
		key, ok := FromString(name)
		if !ok {
			t.Errorf("FromString(%q) not found", name)
		}
		want := KeyF1 + Key(i-1)
		if key != want {
			t.Errorf("FromString(%q) = %d, want %d", name, key, want)
		}
	}
}
