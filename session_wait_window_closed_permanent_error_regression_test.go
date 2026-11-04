package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// A missing session-bound window is a programming error, not a transient
// compositor observation. Retrying until the caller's deadline conceals it.
func TestWaitWindowClosedRejectsMissingWindowHandleImmediately(t *testing.T) {
	session := NewSessionForTesting(nil, nil, newHandleWindowManager(), nil, nil)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	evidence, err := session.WaitWithEvidence(ctx, WindowClosed(nil), WaitEvery(time.Second))
	if !errors.Is(err, ErrNilSession) || evidence.Evaluations != 1 {
		t.Fatalf("missing handle retried as a transient failure: %+v %v", evidence, err)
	}
}

// A backend without stable window IDs cannot ever verify a particular handle.
// Its unsupported capability must fail without a long retry loop.
func TestWaitWindowClosedRejectsUnsupportedStableHandleLookupImmediately(t *testing.T) {
	manager := &flakyWindowManager{}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &Window{id: WindowID{session: session, native: "synthetic-window"}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	evidence, err := session.WaitWithEvidence(ctx, WindowClosed(target), WaitEvery(time.Second))
	if !errors.Is(err, ErrUnsupported) || !errors.Is(err, window.ErrNotSupported) || evidence.Evaluations != 1 {
		t.Fatalf("unsupported stable ID lookup retried: %+v %v", evidence, err)
	}
}

// WindowClosed is a stable-identity query, not a title query. A renamed window
// is still present; disappearance of its native handle is the success proof.
func TestWaitWindowClosedRequiresStableHandleDisappearance(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "opaque-123", Title: "Before"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &Window{id: WindowID{session: session, native: "opaque-123"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	manager.setWindows(false, window.Info{NativeID: "opaque-123", Title: "After"})
	proof, err := WindowClosed(target).evaluate(ctx, session)
	if err != nil || proof {
		t.Fatalf("title-only change falsely proved window closed: proof=%v err=%v", proof, err)
	}
	manager.setWindows(false)
	proof, err = WindowClosed(target).evaluate(ctx, session)
	if err != nil || !proof {
		t.Fatalf("stable window disappearance was not recognized: proof=%v err=%v", proof, err)
	}
	evidence, err := session.WaitWithEvidence(ctx, WindowClosed(target))
	if err != nil || evidence.Evaluations != 1 {
		t.Fatalf("closed window not recognized immediately: %+v %v", evidence, err)
	}
}

// A manager that cannot answer is not evidence that a live window vanished.
func TestWaitWindowClosedPropagatesTransientBackendFailure(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "opaque-123", Title: "Live"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &Window{id: WindowID{session: session, native: "opaque-123"}}
	failure := errors.New("synthetic window transport timeout")
	manager.setError(failure)
	proof, err := WindowClosed(target).evaluate(context.Background(), session)
	if proof || !errors.Is(err, failure) {
		t.Fatalf("failed lookup falsely proved window closed: proof=%v err=%v", proof, err)
	}
	failures := waitEvaluateFailures{}
	proof, err = session.evaluateWaitCondition(context.Background(), WindowClosed(target), &failures)
	if proof || err != nil || failures.total != 1 {
		t.Fatalf("transient manager failure no longer retries: proof=%v err=%v count=%d", proof, err, failures.total)
	}
	manager.setError(nil)
	manager.setWindows(false)
	proof, err = session.evaluateWaitCondition(context.Background(), WindowClosed(target), &failures)
	if err != nil || !proof || failures.consecutive != 0 {
		t.Fatalf("subsequent valid disappearance failed: proof=%v err=%v failures=%+v", proof, err, failures)
	}
}
