package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/window"
)

// focusTestWindowManager models a compositor that observes focus only after
// ActivateByID, rather than treating the activation request as proof of focus.
type focusTestWindowManager struct {
	*handleWindowManager
}

func (m *focusTestWindowManager) ActivateByID(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actions = append(m.actions, "activate:"+id)
	for i := range m.windows {
		m.windows[i].Active = m.windows[i].StableID() == id
	}
	return nil
}

type windowInputSpy struct {
	closeCountingInput
	mu    sync.Mutex
	calls []string
	after func()
}

func (b *windowInputSpy) TypeLiteral(_ context.Context, value string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "literal:"+value)
	b.mu.Unlock()
	if b.after != nil {
		b.after()
	}
	return nil
}

func (b *windowInputSpy) Type(_ context.Context, value string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "keys:"+value)
	b.mu.Unlock()
	if b.after != nil {
		b.after()
	}
	return nil
}

func (b *windowInputSpy) callsSnapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func windowConvenienceFixture(t *testing.T, info ...window.Info) (*Session, *focusTestWindowManager, *windowInputSpy) {
	t.Helper()
	manager := &focusTestWindowManager{handleWindowManager: newHandleWindowManager(info...)}
	backend := &windowInputSpy{}
	session := NewSessionForTesting(nil, input.Inputter(backend), manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	return session, manager, backend
}

func TestWindowTargetActivatesAndChecksEachInput(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor", Active: false,
	})
	if err := session.Window("Journal").Do(Type("text {ctrl+s}+é"), Key("tab")); err != nil {
		t.Fatal(err)
	}
	if got := backend.callsSnapshot(); !reflect.DeepEqual(got, []string{"literal:text {ctrl+s}+é", "keys:{tab}"}) {
		t.Fatalf("dispatches = %v", got)
	}
	if got := manager.actions; !reflect.DeepEqual(got, []string{"activate:office-1"}) {
		t.Fatalf("activations = %v", got)
	}
}

func TestWindowTargetPreflightRejectsLateInvalidStepBeforeActivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor",
	})
	err := session.Window("Journal").Do(Type("secret"), Key("x}{ctrl+a"))
	if !errors.Is(err, ErrInvalidArgument) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("preflight err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetRejectsAmbiguousTitleWithoutInput(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t,
		window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"},
		window.Info{NativeID: "2", Title: "Journal", PID: 43, AppID: "editor"},
	)
	err := session.Window("Journal").Do(Type("private"))
	if !errors.Is(err, ErrWindowAmbiguous) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("ambiguous target err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetFailsBeforeInputIfIdentityUnverifiable(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal",
	})
	err := session.Window("Journal").Do(Type("private"))
	if !errors.Is(err, ErrUnsupported) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unverifiable target err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetStopsOnFocusLossWithoutReactivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	backend.after = func() {
		manager.mu.Lock()
		manager.windows[0].Active = false
		manager.mu.Unlock()
	}
	err := session.Window("Journal").Do(Type("first"), Type("second"))
	var stepErr *StepError
	if !errors.Is(err, ErrWindowFocusUnverified) || !errors.As(err, &stepErr) ||
		stepErr.Index != 1 || stepErr.MayHaveDispatched ||
		len(manager.actions) != 1 || !reflect.DeepEqual(backend.callsSnapshot(), []string{"literal:first"}) {
		t.Fatalf("focus loss err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetDoesNotRetargetRecycledIDWithChangedIdentity(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	backend.after = func() {
		manager.mu.Lock()
		manager.windows[0].PID = 99
		manager.mu.Unlock()
	}
	err := session.Window("Journal").Do(Type("first"), Type("second"))
	if !errors.Is(err, ErrWindowIdentityChanged) || len(manager.actions) != 1 ||
		!reflect.DeepEqual(backend.callsSnapshot(), []string{"literal:first"}) {
		t.Fatalf("retarget err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetFailsClosedWhenFocusCannotBeObserved(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor", Active: false,
	})
	backend := &windowInputSpy{}
	session := NewSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := session.WithContext(ctx).Window("Journal").Do(Type("private"))
	if !errors.Is(err, context.DeadlineExceeded) || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unobserved focus err=%v calls=%v", err, backend.callsSnapshot())
	}
	if got := manager.actions; !reflect.DeepEqual(got, []string{"activate:1"}) {
		t.Fatalf("activation attempts = %v", got)
	}
}

func TestWindowTargetInvalidClosedAndCanceledViews(t *testing.T) {
	var nilTarget *WindowTarget
	if err := nilTarget.Do(Type("text")); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil target: %v", err)
	}
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	if err := session.Window(" ").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank selector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.WithContext(ctx).Window("Journal").Do(Type("text")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled view: %v", err)
	}
	if len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatal("invalid views dispatched input or activation")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Window("Journal").Do(Type("text")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session: %v", err)
	}
}

func TestWindowTargetRejectsGlobalPointerClickBeforeActivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor",
	})
	err := session.Window("Journal").Do(Type("first"), Click(500, 500))
	if !errors.Is(err, ErrUnsupported) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unsafe global click err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

// A real editor often changes its title after the first keystroke.
func TestWindowTargetContinuesAfterTitleChangeOnSameIdentity(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"})
	backend.after = func() { manager.mu.Lock(); manager.windows[0].Title = "*Journal"; manager.mu.Unlock() }
	if err := session.Window("Journal").Do(Type("first"), Type("second")); err != nil {
		t.Fatal(err)
	}
	if got := backend.callsSnapshot(); !reflect.DeepEqual(got, []string{"literal:first", "literal:second"}) {
		t.Fatalf("input after title change: %v", got)
	}
}
