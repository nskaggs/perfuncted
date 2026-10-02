package input

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestInputAttemptsJoinsRecordedCausesInSelectionOrder(t *testing.T) {
	var attempts inputAttempts
	if err := attempts.join(fmt.Errorf("terminal")); err == nil || err.Error() != "terminal" {
		t.Fatalf("join with no recorded causes = %v, want the terminal error", err)
	}

	attempts.add("wl-virtual input", errors.New("zwlr_virtual_pointer_manager_v1 absent"))
	attempts.add("XTEST input", errors.New("cannot open display"))
	attempts.add("uinput input", errors.New("permission denied"))
	attempts.add("ignored", nil)

	err := attempts.join(errors.New("uinput inaccessible"))
	if err == nil {
		t.Fatal("join returned nil")
	}
	message := err.Error()
	for _, want := range []string{
		"wl-virtual input",
		"zwlr_virtual_pointer_manager_v1 absent",
		"XTEST input",
		"cannot open display",
		"uinput input",
		"permission denied",
		"uinput inaccessible",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("joined error missing %q:\n%v", want, message)
		}
	}

	// Selection order is preserved so the most relevant cause reads first.
	first := strings.Index(message, "wl-virtual input")
	second := strings.Index(message, "XTEST input")
	last := strings.Index(message, "uinput inaccessible")
	if first >= second || second >= last {
		t.Fatalf("causes are not in selection order:\n%v", message)
	}

	// The wrapped causes stay inspectable rather than only rendered as text.
	if !strings.Contains(message, "cannot open display") {
		t.Fatalf("joined error lost its causes:\n%v", message)
	}
}

func TestInputAttemptsLabelsEveryRecordedBackend(t *testing.T) {
	// A label must appear for each attempt so an operator can tell which
	// backend refused and why, instead of seeing only the last fallback.
	var attempts inputAttempts
	attempts.add("GNOME Shell bridge input", errors.New("bridge not on the session bus"))
	if got := len(attempts); got != 1 {
		t.Fatalf("recorded %d attempts, want 1", got)
	}
	if !strings.Contains(attempts[0].Error(), "GNOME Shell bridge input") {
		t.Fatalf("attempt %q is missing its label", attempts[0])
	}
	if !strings.Contains(attempts[0].Error(), "bridge not on the session bus") {
		t.Fatalf("attempt %q lost its cause", attempts[0])
	}
}
