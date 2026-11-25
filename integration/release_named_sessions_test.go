//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Exercise the published command boundary, not synthetic registry owners.
// Each owner starts its own Sway and D-Bus processes and must clean them up.
func TestReleaseNamedSessions(t *testing.T) {
	if mustSuite(t).mode != displayHeadlessWayland {
		t.Fatal("named-session acceptance requires headless Wayland")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	binary := filepath.Join(t.TempDir(), "pf")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/pf")
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pf: %v: %s", err, out)
	}
	call := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, binary, append([]string{"session"}, args...)...).CombinedOutput()
	}
	type reply struct {
		ID          string
		Name        string
		State       string
		Environment map[string]string
	}
	type owner struct {
		name   string
		done   chan error
		attach reply
	}
	start := func() owner {
		t.Helper()
		o := owner{name: "release-" + rand.Text()[:16], done: make(chan error, 1)}
		cmd := exec.CommandContext(ctx, binary, "session", "start", "--name", o.name)
		log, err := os.CreateTemp(t.TempDir(), "owner-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			t.Fatal(err)
		}
		go func() { o.done <- cmd.Wait(); close(o.done); _ = log.Close() }()
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
			defer stop()
			_ = exec.CommandContext(cleanupCtx, binary, "session", "stop", o.name).Run()
			select {
			case <-o.done:
			case <-cleanupCtx.Done():
				_ = cmd.Process.Kill()
				<-o.done
				t.Error("named session owner failed to exit during cleanup")
			}
		})
		deadline := time.Now().Add(35 * time.Second)
		for time.Now().Before(deadline) {
			out, err := call("attach", o.name, "--output", "json")
			if err == nil {
				if err := json.Unmarshal(out, &o.attach); err != nil {
					t.Fatalf("attach JSON: %v: %s", err, out)
				}
				if o.attach.State != "ready" || o.attach.Name != o.name || o.attach.ID == "" || len(o.attach.Environment) != 3 {
					t.Fatalf("attach: %+v", o.attach)
				}
				return o
			}
			select {
			case err := <-o.done:
				data, _ := os.ReadFile(log.Name())
				t.Fatalf("named owner exited: %v: %s", err, data)
			case <-time.After(100 * time.Millisecond):
			}
		}
		data, _ := os.ReadFile(log.Name())
		t.Fatalf("named session never became ready: %s", data)
		return o
	}
	probe := func(o owner) {
		t.Helper()
		rt := env.Current().Without("DISPLAY", "WAYLAND_DISPLAY", "WAYLAND_SOCKET", "SWAYSOCK", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "ATSPI_BUS_ADDRESS", "AT_SPI_BUS").With("XDG_SESSION_TYPE", "wayland")
		for key, value := range o.attach.Environment {
			if value == "" {
				t.Fatalf("missing attached %s", key)
			}
			rt = rt.With(key, value)
		}
		cmd := exec.CommandContext(ctx, binary, "screen", "grab", "--rect", "0,0,64,48", "--out", "-")
		cmd.Env = rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("attached screen capture: %v: %s", err, stderr.String())
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil || img.Bounds() != image.Rect(0, 0, 64, 48) {
			t.Fatalf("attached capture: %v", err)
		}
	}
	a, b := start(), start()
	if a.attach.ID == b.attach.ID || a.attach.Environment["XDG_RUNTIME_DIR"] == b.attach.Environment["XDG_RUNTIME_DIR"] ||
		a.attach.Environment["DBUS_SESSION_BUS_ADDRESS"] == b.attach.Environment["DBUS_SESSION_BUS_ADDRESS"] {
		t.Fatal("named sessions are not isolated")
	}
	probe(a)
	probe(b)
	listed, err := call("list", "--output", "json")
	if err != nil {
		t.Fatalf("session list: %v: %s", err, listed)
	}
	var rows []reply
	if err := json.Unmarshal(listed, &rows); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, row := range rows {
		if row.State == "ready" {
			seen[row.Name] = row.ID
		}
	}
	if seen[a.name] != a.attach.ID || seen[b.name] != b.attach.ID {
		t.Fatalf("list omitted owners: %s", listed)
	}
	if out, err := call("recover", a.name); err == nil {
		t.Fatalf("recovered active session: %s", out)
	}
	if out, err := call("start", "--name", a.name); err == nil {
		t.Fatalf("duplicate owner accepted: %s", out)
	}
	out, err := call("stop", a.name, "--output", "json")
	var stopped reply
	if err != nil {
		t.Fatalf("stop: %v: %s", err, out)
	}
	if err := json.Unmarshal(out, &stopped); err != nil || stopped.State != "stopped" || stopped.ID != a.attach.ID {
		t.Fatalf("stop receipt: %s, %v", out, err)
	}
	select {
	case err := <-a.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stopped owner remained alive")
	}
	if out, err := call("attach", a.name); err == nil {
		t.Fatalf("stopped session still attachable: %s", out)
	}
	probe(b)
}
