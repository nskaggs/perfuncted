package input

import (
	"strings"
	"testing"

	"github.com/bendahl/uinput"
)

func TestRemoteDesktopKeycodeMappingsCoverModifiersNavigationAndFunctionKeys(t *testing.T) {
	tests := []struct {
		name string
		want uint32
	}{
		{"a", uinput.KeyA}, {"z", uinput.KeyZ}, {"0", uinput.Key0}, {"9", uinput.Key9},
		{"f1", uinput.KeyF1}, {"f4", uinput.KeyF4}, {"f10", uinput.KeyF10},
		{"f11", uinput.KeyF11}, {"f12", uinput.KeyF12},
		{"space", uinput.KeySpace}, {"enter", uinput.KeyEnter},
		{"tab", uinput.KeyTab}, {"backspace", uinput.KeyBackspace}, {"esc", uinput.KeyEsc},
		{"ctrl", uinput.KeyLeftctrl}, {"alt", uinput.KeyLeftalt},
		{"shift", uinput.KeyLeftshift}, {"super", uinput.KeyLeftmeta},
		{"up", uinput.KeyUp}, {"down", uinput.KeyDown},
		{"left", uinput.KeyLeft}, {"right", uinput.KeyRight},
		{"home", uinput.KeyHome}, {"end", uinput.KeyEnd},
		{"pageup", uinput.KeyPageup}, {"pagedown", uinput.KeyPagedown},
		{"insert", uinput.KeyInsert}, {"delete", uinput.KeyDelete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, err := remoteKeyCode(tc.name)
			if err != nil || code != tc.want {
				t.Fatalf("remote key %q = %d, %v want %d", tc.name, code, err, tc.want)
			}
		})
	}
	for _, invalid := range []string{"", "  ", "DefinitelyNotAKeyboardKey", "not-real", "🔑"} {
		if code, err := remoteKeyCode(invalid); err == nil {
			t.Fatalf("invalid key name %q mapped to %d", invalid, code)
		}
	}
	if upper, err := remoteKeyCode(" A "); err != nil || upper != uinput.KeyA {
		t.Fatalf("trimmed upper-case a should map to key A: %d %v", upper, err)
	}
	if code, err := remoteKeyCode(strings.ToUpper("f12")); err != nil || code != uinput.KeyF12 {
		t.Fatalf("uppercase function key mapping %d, %v", code, err)
	}
}
