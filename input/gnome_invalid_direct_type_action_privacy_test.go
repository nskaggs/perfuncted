package input

import (
	"context"
	"strings"
	"testing"
)

// The parsed Type entrypoint validates keys before calling typeAction, but
// typeAction itself must reject an unrecognized key before touching modifier
// ownership or forwarding any synthetic key event to the desktop bridge.
func TestGnomeTypeActionRejectsUnknownKeyBeforeModifierDispatch(t *testing.T) {
	for _, name := range []string{"not-a-real-key", "", string([]byte{0xff})} {
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			held := modifiers{shift: true}
			owned := modifiers{ctrl: true}
			err := backend.typeAction(context.Background(), keySend{key: name, modifiers: modifiers{ctrl: true, alt: true, super: true}}, &held, &owned)
			if err == nil {
				t.Fatalf("unknown key %q accepted", name)
			}
			if len(bridge.events) != 0 {
				t.Fatalf("unrecognized key %q dispatched events: %v", name, bridge.events)
			}
			if held != (modifiers{shift: true}) || owned != (modifiers{ctrl: true}) {
				t.Fatalf("unrecognized key changed modifier ownership: held=%+v owned=%+v", held, owned)
			}
			if name == string([]byte{0xff}) && !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid encoding error not preserved: %v", err)
			}
		})
	}
}
