//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/window"
)

func certifyReleaseWindowLifecycle(t *testing.T, ctx context.Context, s *suite, binary string, target *perfuncted.Window) {
	t.Helper()
	info, err := target.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, stderr.String())
		}
		return strings.TrimSpace(string(out))
	}
	for _, args := range [][]string{{"window", "find", info.Title}, {"window", "find-by-title", info.Title}, {"window", "wait-for", info.Title, "--timeout", "3s"}} {
		if got := run(args...); !strings.Contains(got, info.Title) {
			t.Fatalf("window discovery %v: %q", args, got)
		}
	}
	if got := run("window", "get-geometry", info.Title); got != fmt.Sprintf("%d,%d,%d,%d", info.X, info.Y, info.X+info.W, info.Y+info.H) {
		t.Fatalf("window geometry: %q", got)
	}
	if got := run("window", "is-visible", info.Title); got != "true" {
		t.Fatalf("visible editor: %q", got)
	}
	run("window", "activate-id", info.StableID(), "--verify", "--verify-timeout", "3s")
	if got := run("window", "active"); got != info.Title {
		t.Fatalf("active editor: %q", got)
	}
	var active window.Info
	if err := json.Unmarshal([]byte(run("window", "active-info")), &active); err != nil || active.StableID() != info.StableID() || !active.Active {
		t.Fatalf("CLI active metadata: %+v %v", active, err)
	}
	var windows []window.Info
	if err := json.Unmarshal([]byte(run("window", "list", "--output", "json")), &windows); err != nil || !hasReleaseWindow(windows, info.StableID()) {
		t.Fatalf("CLI window listing: %+v %v", windows, err)
	}
	watchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(watchCtx, binary, "window", "watch", "--output", "json")
	cmd.Env = s.rt.EnvList()
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	decoder := json.NewDecoder(stdout)
	type event struct {
		Windows []window.Info
		Count   int
	}
	var initial event
	if err := decoder.Decode(&initial); err != nil || initial.Count != len(initial.Windows) || !hasReleaseWindow(initial.Windows, info.StableID()) {
		t.Fatalf("initial window event: %+v %v", initial, err)
	}
	run("window", "close-id", info.StableID(), "--verify", "--verify-timeout", "3s")
	for {
		var changed event
		if err := decoder.Decode(&changed); err != nil || changed.Count != len(changed.Windows) {
			t.Fatalf("window removal event: %+v %v", changed, err)
		}
		if !hasReleaseWindow(changed.Windows, info.StableID()) {
			break
		}
	}
	run("window", "wait-close", info.Title, "--timeout", "3s")
	if got := run("window", "is-visible", info.Title); got != "false" {
		t.Fatalf("closed editor still visible: %q", got)
	}
}
func hasReleaseWindow(windows []window.Info, id string) bool {
	for _, w := range windows {
		if w.StableID() == id {
			return true
		}
	}
	return false
}
