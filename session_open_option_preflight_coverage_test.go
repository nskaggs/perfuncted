package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// An invalid context must be refused before configuration or desktop routing.
func TestOpenNilContextDoesNotEvaluateOptions(t *testing.T) {
	called := false
	session, err := Open(nil, Option(func(*openConfig) error {
		called = true
		return nil
	}))
	if session != nil || !errors.Is(err, ErrInvalidArgument) || called {
		t.Fatalf("nil-context Open returned session=%v err=%v evaluatedOptions=%v", session, err, called)
	}
}

// Invalid configuration must stop option evaluation before any runtime or
// backend setup; a later option cannot turn an error into success.
func TestOpenOptionFailureStopsConfigurationBeforeRuntime(t *testing.T) {
	rejected := errors.New("synthetic rejected desktop configuration")
	laterCalled := false
	session, err := Open(context.Background(),
		Option(func(*openConfig) error { return rejected }),
		Option(func(*openConfig) error { laterCalled = true; return nil }),
	)
	if session != nil || !errors.Is(err, rejected) || laterCalled {
		t.Fatalf("failed option opened session=%v err=%v laterCalled=%v", session, err, laterCalled)
	}
}

// A configuration option can end the owner's permission to start a desktop.
func TestOpenOptionCancellationStopsRuntimeSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := Open(ctx, Option(func(*openConfig) error {
		cancel()
		return nil
	}))
	if session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("configuration cancellation opened session=%v err=%v", session, err)
	}
}

// Closing a session revokes direct paste authority independently of whether
// clipboard and keyboard backends were ever requested.
func TestClosedSessionPasteRejectsWithoutDesktopDispatch(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Paste(context.Background(), "private selection"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted paste: %v", err)
	}
}
