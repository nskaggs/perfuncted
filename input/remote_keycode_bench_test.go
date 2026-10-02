package input

import "testing"

// Resolving a key name to an evdev code happens on every keystroke of a portal
// RemoteDesktop session.
func BenchmarkRemoteKeyCode(b *testing.B) {
	for _, name := range []string{"a", "q", "z", "0", "9", "space", "enter", "Return"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := remoteKeyCode(name); err != nil {
					b.Fatalf("remoteKeyCode(%q): %v", name, err)
				}
			}
		})
	}
}

// A literal character lookup takes the rune-table path rather than the key-name
// path.
func BenchmarkRemoteKeyCodeRune(b *testing.B) {
	for _, r := range []rune{'a', 'Z', '7', '!'} {
		b.Run(string(r), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := remoteKeyCode(string(r)); err != nil {
					b.Fatalf("remoteKeyCode(%q): %v", r, err)
				}
			}
		})
	}
}

func BenchmarkQwertyRuneMap(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if len(qwertyRuneMap()) == 0 {
			b.Fatal("empty rune map")
		}
	}
}
