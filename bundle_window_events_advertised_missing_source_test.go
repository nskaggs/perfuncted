package perfuncted

import (
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

type advertisedEventsWithoutSource struct{ *handleWindowManager }

func (*advertisedEventsWithoutSource) SupportedOperations() []string {
	return []string{"events", "discover"}
}

// A backend's static capability advertisement is not proof it implements an
// optional event subscription. Never expose a fabricated stream or succeed
// when the source interface is absent.
func TestWindowEventsAdvertisedWithoutSourceFailClosed(t *testing.T) {
	manager := &advertisedEventsWithoutSource{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	events, err := session.Windows.Events()
	if events != nil || !errors.Is(err, ErrUnsupported) || !errors.Is(err, window.ErrNotSupported) {
		t.Fatalf("advertised but unimplemented events: stream=%v err=%v", events, err)
	}
}
