package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

type typedNilClipboard struct{}

func (*typedNilClipboard) Get(context.Context) (string, error) {
	return "", nil
}

func (*typedNilClipboard) Set(context.Context, string) error { return nil }

func (*typedNilClipboard) Close() error { return nil }

type typedNilOutputLister struct{}

func (*typedNilOutputLister) List(context.Context) ([]output.Info, error) {
	return nil, nil
}

func (*typedNilOutputLister) Close() error { return nil }

func TestBundleAvailabilityTreatsTypedNilBackendsAsUnavailable(t *testing.T) {
	var screenshotter *screen.X11Backend
	var inputter *input.XTestBackend
	var manager *window.SwayManager
	var lister *typedNilOutputLister
	var clipboardBackend *typedNilClipboard

	tests := []struct {
		name  string
		check func(*Session) error
	}{
		{
			name: "screen",
			check: func(session *Session) error {
				bundle := &ScreenBundle{
					backend: screenshotter,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityScreen,
					},
				}
				_, err := bundle.Grab(context.Background(), image.Rectangle{})
				return err
			},
		},
		{
			name: "input",
			check: func(session *Session) error {
				bundle := &InputBundle{
					backend: inputter,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityInput,
					},
				}
				return bundle.KeyDown(context.Background(), "a")
			},
		},
		{
			name: "windows",
			check: func(session *Session) error {
				bundle := &WindowBundle{
					backend: manager,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityWindows,
					},
				}
				_, err := bundle.List(context.Background(), WindowMatch{})
				return err
			},
		},
		{
			name: "outputs",
			check: func(session *Session) error {
				bundle := &OutputBundle{
					backend: lister,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityOutputs,
					},
				}
				_, err := bundle.List(context.Background())
				return err
			},
		},
		{
			name: "clipboard",
			check: func(session *Session) error {
				bundle := &ClipboardBundle{
					backend: clipboardBackend,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityClipboard,
					},
				}
				_, err := bundle.Get(context.Background())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.check(bundleAvailabilityTestSession())
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("typed-nil backend error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func bundleAvailabilityTestSession() *Session {
	return &Session{ctx: context.Background()}
}

var (
	_ clipboard.Clipboard  = (*typedNilClipboard)(nil)
	_ input.Inputter       = (*input.XTestBackend)(nil)
	_ output.Lister        = (*typedNilOutputLister)(nil)
	_ screen.Screenshotter = (*screen.X11Backend)(nil)
	_ window.Manager       = (*window.SwayManager)(nil)
)
