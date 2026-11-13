package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type singleActionAuthorityBackend struct {
	pftest.Inputter
	after func()
	calls int
}

func (b *singleActionAuthorityBackend) dispatch() error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *singleActionAuthorityBackend) KeyDown(context.Context, string) error { return b.dispatch() }
func (b *singleActionAuthorityBackend) KeyUp(context.Context, string) error   { return b.dispatch() }
func (b *singleActionAuthorityBackend) Type(context.Context, string) error    { return b.dispatch() }
func (b *singleActionAuthorityBackend) TypeLiteral(context.Context, string) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseMove(context.Context, int, int) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseClick(context.Context, int, int, int) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseDown(context.Context, int) error   { return b.dispatch() }
func (b *singleActionAuthorityBackend) MouseUp(context.Context, int) error     { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollUp(context.Context, int) error    { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollDown(context.Context, int) error  { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollLeft(context.Context, int) error  { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollRight(context.Context, int) error { return b.dispatch() }

// A non-cooperative input provider must not receive pre-canceled events or
// cause the facade to acknowledge completion after the caller or session
// revokes authority. All individual injection methods share this contract.
func TestSingleInputActionsRejectAuthorizationLoss(t *testing.T) {
	actions := []struct {
		name string
		run  func(*perfuncted.Session, context.Context) error
	}{
		{"key-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.KeyDown(c, "a") }},
		{"key-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.KeyUp(c, "a") }},
		{"type", func(s *perfuncted.Session, c context.Context) error { return s.Input.Type(c, "hello") }},
		{"type-literal", func(s *perfuncted.Session, c context.Context) error { return s.Input.TypeLiteral(c, "hello") }},
		{"mouse-move", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseMove(c, 1, 2) }},
		{"mouse-click", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseClick(c, 1, 2, 1) }},
		{"mouse-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseDown(c, 1) }},
		{"mouse-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseUp(c, 1) }},
		{"scroll-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollUp(c, 1) }},
		{"scroll-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollDown(c, 1) }},
		{"scroll-left", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollLeft(c, 1) }},
		{"scroll-right", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollRight(c, 1) }},
	}
	for _, action := range actions {
		for _, mode := range []string{"pre-canceled", "late-canceled", "session-closed"} {
			t.Run(action.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &singleActionAuthorityBackend{}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := error(context.Canceled)
				switch mode {
				case "pre-canceled":
					cancel()
				case "late-canceled":
					backend.after = cancel
				case "session-closed":
					expected = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := action.run(session, ctx)
				calls := 1
				if mode == "pre-canceled" {
					calls = 0
				}
				if !errors.Is(err, expected) || backend.calls != calls {
					t.Fatalf("%s/%s: err=%v backend calls=%d, expected error=%v calls=%d", action.name, mode, err, backend.calls, expected, calls)
				}
			})
		}
	}
}
