package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A caller that stops a managed process directly must escalate from TERM to
// KILL when the process ignores TERM. Do not require Session.Close to perform
// escalation or report the expected forced exit as a shutdown failure.
func TestStopApplicationEscalatesIgnoredTERMAndReapsProcess(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.config.ApplicationGracePeriod = 80 * time.Millisecond
	t.Cleanup(func() { _ = session.Close() })

	ready := filepath.Join(t.TempDir(), "ready")
	app, err := session.Launch(context.Background(), Command{
		Name: "sh",
		Args: []string{"-c", `trap '' TERM; : > "$PF_READY"; while :; do :; done`},
		Env:  []string{"PF_READY=" + ready},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	// Ensure the child has installed its TERM handler before sending a signal.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("read ready marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("child never installed its TERM handler")
		}
		time.Sleep(time.Millisecond)
	}

	if err := session.stopApplication(app); err != nil {
		t.Fatalf("stop application: %v", err)
	}
	if app.proc.groupAlive() {
		t.Fatal("TERM-resistant application process group survived escalation")
	}
	if !app.Exited() {
		t.Fatal("process leader not reaped after escalation")
	}
	// Repeated stop cannot signal a newly allocated process group with the
	// reused numeric PID after the original process has already terminated.
	if err := session.stopApplication(app); err != nil {
		t.Fatalf("repeat stop: %v", err)
	}
}
