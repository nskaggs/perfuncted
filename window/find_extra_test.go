package window

import (
	"context"
	"testing"
	"time"

	pollpkg "github.com/nskaggs/perfuncted/poll"
)

// ── Clamp ─────────────────────────────────────────────────────────────────────

func TestClamp_Zero(t *testing.T) {
	got := pollpkg.Clamp(0)
	if got != 10*time.Millisecond {
		t.Fatalf("Clamp(0) = %v, want 10ms", got)
	}
}

func TestClamp_Negative(t *testing.T) {
	got := pollpkg.Clamp(-5 * time.Millisecond)
	if got != 10*time.Millisecond {
		t.Fatalf("Clamp(-5ms) = %v, want 10ms", got)
	}
}

func TestClamp_Positive(t *testing.T) {
	got := pollpkg.Clamp(20 * time.Millisecond)
	if got != 20*time.Millisecond {
		t.Fatalf("Clamp(20ms) = %v, want 20ms", got)
	}
}

// ── WaitFor ───────────────────────────────────────────────────────────────────

func TestWaitFor_FindsWindowImmediately(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "MyApp"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	w, err := WaitFor(ctx, m, "myapp", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitFor returned unexpected error: %v", err)
	}
	if w.ID != 1 {
		t.Fatalf("WaitFor returned ID %d, want 1", w.ID)
	}
}

func TestWaitFor_Timeout(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "SomethingElse"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitFor(ctx, m, "neverfound", 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitFor expected timeout error, got nil")
	}
}

// ── WaitForClose ──────────────────────────────────────────────────────────────

func TestWaitForClose_SucceedsWhenAbsent(t *testing.T) {
	// Window is not present — WaitForClose should return immediately.
	m := &fakeManager{wins: []Info{{ID: 1, Title: "OtherWindow"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := WaitForClose(ctx, m, "neverpresent", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForClose returned unexpected error: %v", err)
	}
}

func TestWaitForClose_Timeout(t *testing.T) {
	m := &fakeManager{wins: []Info{{ID: 1, Title: "StillOpen"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := WaitForClose(ctx, m, "stillopen", 10*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForClose expected timeout error, got nil")
	}
}
