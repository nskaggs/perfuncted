package perfuncted

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

type listOnlyWindowManager struct {
	infos []window.Info
}

func (m *listOnlyWindowManager) List(context.Context) ([]window.Info, error) {
	return m.infos, nil
}

func (m *listOnlyWindowManager) IterateWindows(context.Context) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		for _, info := range m.infos {
			if !yield(info, nil) {
				return
			}
		}
	}
}

func (m *listOnlyWindowManager) ActiveTitle(context.Context) (string, error) {
	if len(m.infos) == 0 {
		return "", window.ErrWindowNotFound
	}
	return m.infos[0].Title, nil
}

func (m *listOnlyWindowManager) Close() error { return nil }

func TestOperationErrorMapsBackendSentinels(t *testing.T) {
	t.Parallel()
	base := bundleBase{capability: CapabilityScreen}
	tests := []struct {
		name      string
		cause     error
		wantCause error
		wantFail  bool
	}{
		{name: "input unsupported", cause: input.ErrNotSupported, wantCause: ErrUnsupported},
		{name: "window unsupported", cause: window.ErrNotSupported, wantCause: ErrUnsupported},
		{name: "accessibility unsupported", cause: accessibility.ErrUnsupported, wantCause: ErrUnsupported},
		{name: "accessibility correlation unsupported", cause: accessibility.ErrUnsupportedCorrelation, wantCause: ErrUnsupported},
		{name: "capability unsupported", cause: capability.Unsupported("output", "wayland", "no globals"), wantCause: ErrUnsupported},
		{name: "gnome bridge unavailable", cause: gnomebridge.ErrUnavailable, wantCause: ErrUnavailable},
		{name: "clipboard tool unavailable", cause: clipboard.ErrNoClipboardTool, wantCause: ErrUnavailable},
		{name: "util not available", cause: util.ErrNotAvailable, wantCause: ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := base.operationError("test", test.cause)
			var operationErr *OperationError
			if !errors.As(err, &operationErr) {
				t.Fatalf("error = %v, want *OperationError", err)
			}
			if !errors.Is(err, test.wantCause) {
				t.Fatalf("error = %v, want %v", err, test.wantCause)
			}
			if got := errors.Is(err, ErrOperationFailed); got != test.wantFail {
				t.Fatalf("errors.Is(ErrOperationFailed) = %v, want %v; err = %v", got, test.wantFail, err)
			}
		})
	}
}

func TestUtilCheckAvailableReturnsSentinel(t *testing.T) {
	t.Parallel()
	err := util.CheckAvailable("screen", nil)
	if !errors.Is(err, util.ErrNotAvailable) {
		t.Fatalf("CheckAvailable error = %v, want ErrNotAvailable", err)
	}
	base := bundleBase{capability: CapabilityScreen}
	wrapped := base.operationError("resolution", err)
	if !errors.Is(wrapped, ErrUnavailable) {
		t.Fatalf("wrapped error = %v, want ErrUnavailable", wrapped)
	}
}

func TestWindowFindReturnsOperationError(t *testing.T) {
	t.Parallel()
	session := NewSessionForTesting(nil, nil, &listOnlyWindowManager{}, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	_, err := session.Windows.Find(context.Background(), WindowMatch{TitleContains: "missing"})
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("Find error = %v, want *OperationError", err)
	}
	if !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("Find error = %v, want ErrWindowNotFound", err)
	}
	if !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("Find error = %v, want ErrOperationFailed", err)
	}

	ambiguous := &listOnlyWindowManager{infos: []window.Info{{Title: "dup", NativeID: "1"}, {Title: "dup", NativeID: "2"}}}
	session2 := NewSessionForTesting(nil, nil, ambiguous, nil, nil)
	t.Cleanup(func() { _ = session2.Close() })
	_, err = session2.Windows.Find(context.Background(), WindowMatch{TitleContains: "dup"})
	if !errors.As(err, &operationErr) {
		t.Fatalf("ambiguous Find error = %v, want *OperationError", err)
	}
	if !errors.Is(err, ErrWindowAmbiguous) {
		t.Fatalf("ambiguous Find error = %v, want ErrWindowAmbiguous", err)
	}
}

func TestWindowStableHandleUnsupportedIsOperationError(t *testing.T) {
	t.Parallel()
	session := NewSessionForTesting(nil, nil, &listOnlyWindowManager{infos: []window.Info{{Title: "one", NativeID: "1"}}}, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	found, err := session.Windows.Find(context.Background(), WindowMatch{TitleContains: "one"})
	if err != nil {
		t.Fatalf("Find error = %v", err)
	}
	err = found.Activate(context.Background())
	var operationErr *OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("Activate error = %v, want *OperationError", err)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Activate error = %v, want ErrUnsupported", err)
	}
}

func TestPasteWithoutInputReturnsCapabilityError(t *testing.T) {
	t.Parallel()
	session := NewSessionForTesting(&capabilityScreen{}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.Input = nil
	err := session.Paste(context.Background(), "text")
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("Paste error = %v, want *CapabilityError", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Paste error = %v, want ErrUnavailable", err)
	}
}

func TestAccessibilityActionWithoutBundleReturnsCapabilityError(t *testing.T) {
	t.Parallel()
	session := NewSessionForTesting(&capabilityScreen{}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.Accessibility = nil
	_, _, err := session.InvokeAccessibilityActionAndWait(context.Background(), accessibility.NodeID{}, accessibility.Query{}, "activate", accessibility.SnapshotOptions{}, Predicate("test", func(context.Context) (bool, error) { return true, nil }))
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("InvokeAccessibilityActionAndWait error = %v, want *CapabilityError", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("InvokeAccessibilityActionAndWait error = %v, want ErrUnavailable", err)
	}
}

func TestClipboardPasteWithoutInputReturnsCapabilityError(t *testing.T) {
	t.Parallel()
	session := NewSessionForTesting(nil, nil, nil, nil, &capabilityClipboard{})
	t.Cleanup(func() { _ = session.Close() })
	err := session.Clipboard.pasteWithInputContext(context.Background(), "text", nil)
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) {
		t.Fatalf("pasteWithInputContext error = %v, want *CapabilityError", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pasteWithInputContext error = %v, want ErrUnavailable", err)
	}
}
