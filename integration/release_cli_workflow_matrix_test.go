//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

// The actual release executable, independent clipboard clients, a real editor
// and the disk file form separate verification boundaries on every display.
func TestReleaseCLIWorkflowMatrix(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "pf")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/pf")
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	run := func(stdin string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		cmd.Stdin = strings.NewReader(stdin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, stderr.String())
		}
		return out
	}
	t.Run("native-clipboard-interoperability", func(t *testing.T) {
		read, write := []string{"wl-paste", "--no-newline"}, []string{"wl-copy"}
		if s.mode == displayHeadlessX11 || s.mode == displayNestedX11 {
			read, write = []string{"xclip", "-selection", "clipboard", "-out"}, []string{"xclip", "-selection", "clipboard", "-in"}
		}
		native := func(value string, args []string) []byte {
			t.Helper()
			cmd := exec.CommandContext(ctx, args[0], args[1:]...)
			cmd.Env = s.rt.EnvList()
			cmd.Stdin = strings.NewReader(value)
			// The clipboard owner can outlive its launching process, so don't
			// let a background owner inherit an unread command output pipe.
			if len(args) == len(write) && args[0] == write[0] && args[len(args)-1] == write[len(write)-1] {
				cmd.Stdout = os.Stderr
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("native clipboard: %v", err)
			}
			return out
		}
		for _, value := range []string{"  tabs\tand spaces  ", "line one\nline two\n", "café 世界 🦀 e\u0301", strings.Repeat("data 世界 ", 600)} {
			run("", "clipboard", "set", value)
			if got := string(native("", read)); got != value {
				t.Fatalf("native reader differs: got %d bytes want %d", len(got), len(value))
			}
			other := "native:" + value
			native(other, write)
			if got := string(run("", "clipboard", "get")); got != other {
				t.Fatalf("CLI reader differs: got %d bytes want %d", len(got), len(other))
			}
		}
	})
	t.Run("go-and-controlled-script-save-real-document", func(t *testing.T) {
		doc := filepath.Join(t.TempDir(), "release-"+rand.Text()[:12]+".txt")
		if err := os.WriteFile(doc, nil, 0600); err != nil {
			t.Fatal(err)
		}
		app := appSpec{name: "featherpad", launch: []string{"featherpad"}, winMatch: filepath.Base(doc), saveFile: doc}
		cmd, err := launchApp(s.rt, app, app.extraEnvFor(s.mode)...)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { terminateCmd(cmd, 5*time.Second) })
		target, err := s.pf.Windows.Wait(ctx, perfuncted.WindowMatch{TitleContains: app.winMatch}, perfuncted.WaitEvery(100*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		info, err := target.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		// The Go convenience facade and the separately built script runner
		// operate on the same document; only exact file contents certify success.
		if err := s.pf.WithContext(ctx).Window(info.Title).Do(perfuncted.Type("GoPrefix")); err != nil {
			t.Fatal(err)
		}
		outPath := filepath.Join(t.TempDir(), "captured.png")
		script := filepath.Join(t.TempDir(), "workflow.pf")
		lines := []string{"window select " + app.winMatch, "window activate", "input type-literal ScriptSuffix", "input type {ctrl+s}", "screen grab --rect 10,20,42,44 --out " + outPath}
		if err := os.WriteFile(script, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		id := "release-" + rand.Text()[:16]
		run("", "run", script, "--workflow-id", id)
		var status struct {
			ID                string `json:"id"`
			State             string `json:"state"`
			LastCompletedStep int    `json:"last_completed_step"`
		}
		if err := json.Unmarshal(run("", "workflow", "status", id, "--output", "json"), &status); err != nil || status.ID != id || status.State != "completed" || status.LastCompletedStep != len(lines) {
			t.Fatalf("terminal workflow receipt: %+v, %v", status, err)
		}
		want := "GoPrefixScriptSuffix"
		got, err := waitForFileContains(ctx, doc, want, 10*time.Second)
		if err != nil || strings.TrimSuffix(got, "\n") != want {
			t.Fatalf("independent document contents %q: %v", got, err)
		}
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil || img.Bounds() != image.Rect(0, 0, 32, 24) {
			t.Fatalf("script PNG: %v", err)
		}
		// A second controlled run with the same ID exercises stale-terminal
		// recovery and must publish a new step count rather than replay history.
		if err := os.WriteFile(script, []byte("screen hash --rect 10,20,42,44\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(run("", "run", script, "--workflow-id", id))); len(got) != 8 {
			t.Fatalf("script hash: %q", got)
		}
		if err := json.Unmarshal(run("", "workflow", "status", id, "--output", "json"), &status); err != nil || status.State != "completed" || status.LastCompletedStep != 1 {
			t.Fatalf("reused workflow: %+v, %v", status, err)
		}
		// Explicit cleanup of our unique, completed test-only reservation.
		path := filepath.Join(fmt.Sprintf("/tmp/pf-workflows-%d", os.Geteuid()), id)
		t.Cleanup(func() {
			if err := os.RemoveAll(path); err != nil {
				t.Error(err)
			}
		})
	})
}
