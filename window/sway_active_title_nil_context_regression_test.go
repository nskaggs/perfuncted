package window

import "testing"

// Public window lookups accept nil as the default context. In particular,
// cancellation checks after a successful IPC tree read must not dereference
// the original nil argument.
func TestSwayActiveTitleNilContextUsesDefaultWithoutPanic(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
	defer func() {
		if _, err := finish(); err != nil {
			t.Error(err)
		}
	}()
	title, err := m.ActiveTitle(nil)
	if err != nil || title != "Editor" {
		t.Fatalf("nil-context active title=%q err=%v", title, err)
	}
}
