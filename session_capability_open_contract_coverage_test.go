package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type trackedCapabilityBackend struct{ closes int }

func (b *trackedCapabilityBackend) Close() error {
	b.closes++
	return errors.New("synthetic close failure")
}

func TestCapabilityOpenRejectsNilBackendAndCleansFailedPartialBackend(t *testing.T) {
	t.Run("nil result never installed", func(t *testing.T) {
		called := 0
		backend, err := openCapabilityBackend(context.Background(), time.Second, CapabilityScreen,
			func(context.Context) (*trackedCapabilityBackend, error) { return nil, nil },
			func(*trackedCapabilityBackend) { called++ }, func(*trackedCapabilityBackend) { called++ })
		if backend != nil || !errors.Is(err, ErrUnavailable) || called != 0 {
			t.Fatalf("nil result %v, %v, callbacks %d", backend, err, called)
		}
	})
	t.Run("partially opened failed backend is closed once", func(t *testing.T) {
		partial := &trackedCapabilityBackend{}
		cause := errors.New("open transport failure")
		called := 0
		backend, err := openCapabilityBackend(context.Background(), time.Second, CapabilityInput,
			func(context.Context) (*trackedCapabilityBackend, error) { return partial, cause },
			func(*trackedCapabilityBackend) { called++ }, func(*trackedCapabilityBackend) { called++ })
		if backend != partial || !errors.Is(err, cause) || partial.closes != 1 || called != 0 {
			t.Fatalf("failed result %v, %v; closes=%d callbacks=%d", backend, err, partial.closes, called)
		}
	})
	t.Run("successful backend configured installed and retained", func(t *testing.T) {
		successful := &trackedCapabilityBackend{}
		order := ""
		got, err := openCapabilityBackend(context.Background(), time.Second, CapabilityWindows,
			func(context.Context) (*trackedCapabilityBackend, error) { order += "open "; return successful, nil },
			func(*trackedCapabilityBackend) { order += "configure " }, func(*trackedCapabilityBackend) { order += "install" })
		if err != nil || got != successful || order != "open configure install" || successful.closes != 0 {
			t.Fatalf("success got %v err %v order %q closes %d", got, err, order, successful.closes)
		}
	})
}

func TestCapabilityOpenRespectsContextAndStartupTimeout(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name          string
		createContext func() context.Context
		timeout       time.Duration
		want          error
	}{
		{"already canceled", func() context.Context { return canceled }, time.Second, context.Canceled},
		{"expired startup", context.Background, 0, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed := false
			got, err := openCapabilityBackend(tc.createContext(), tc.timeout, CapabilityClipboard,
				func(ctx context.Context) (*trackedCapabilityBackend, error) { return nil, ctx.Err() }, nil,
				func(*trackedCapabilityBackend) { installed = true })
			if got != nil || !errors.Is(err, tc.want) || installed {
				t.Fatalf("canceled open got %v err %v installed=%t", got, err, installed)
			}
		})
	}
}

func TestCloseFailedBackendIgnoresNonClosableAndTypedNil(t *testing.T) {
	var typedNil *trackedCapabilityBackend
	closeFailedBackend(typedNil, errors.New("failure"))
	closeFailedBackend(struct{}{}, errors.New("failure"))
	complete := &trackedCapabilityBackend{}
	closeFailedBackend(complete, nil)
	if complete.closes != 0 {
		t.Fatal("success backend closed")
	}
	closeFailedBackend(complete, errors.New("failure"))
	if complete.closes != 1 {
		t.Fatalf("failed backend closed %d times", complete.closes)
	}
	if got := nilBackendError(CapabilityScreen); !errors.Is(got, ErrUnavailable) || !strings.Contains(got.Error(), "screen") {
		t.Fatalf("nil backend error %v", got)
	}
}
