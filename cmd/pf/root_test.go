package main

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"github.com/spf13/cobra"
)

func TestNewRootCmdConfiguresCobra(t *testing.T) {
	cmd := newRootCmd(func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) { return nil, nil }
	})

	if got, want := cmd.Use, "pf"; got != want {
		t.Fatalf("Use = %q, want %q", got, want)
	}
	if !cmd.SilenceUsage {
		t.Fatal("SilenceUsage = false")
	}
	if !cmd.SilenceErrors {
		t.Fatal("SilenceErrors = false")
	}

	wantSubs := map[string]bool{
		"screen":    true,
		"input":     true,
		"window":    true,
		"find":      true,
		"output":    true,
		"run":       true,
		"clipboard": true,
		"info":      true,
		"session":   true,
		"docs":      true,
		"version":   true,
	}
	for _, sub := range cmd.Commands() {
		delete(wantSubs, sub.Name())
	}
	if len(wantSubs) != 0 {
		t.Fatalf("missing subcommands: %v", wantSubs)
	}
}

func findCommandPath(cmd *cobra.Command, path ...string) *cobra.Command {
	if len(path) == 0 {
		return cmd
	}
	for _, sub := range cmd.Commands() {
		if sub.Name() == path[0] {
			return findCommandPath(sub, path[1:]...)
		}
	}
	return nil
}

func TestCLICommandTreeIncludesUniqueFeatures(t *testing.T) {
	root := newRootCmd(func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) { return nil, nil }
	})

	for _, path := range [][]string{
		{"screen", "hash"},
		{"screen", "grab-region"},
		{"screen", "get-all-pixels"},
		{"screen", "watch"},
		{"find", "wait-for-visible-change"},
		{"window", "get-geometry"},
		{"window", "is-visible"},
		{"window", "watch"},
		{"output", "list"},
		{"run"},
		{"info"},
		{"session"},
		{"clipboard", "get"},
		{"clipboard", "set"},
	} {
		if got := findCommandPath(root, path...); got == nil {
			t.Fatalf("missing command path %q", strings.Join(path, " "))
		}
	}
}

func TestRunWithFactoryCapturesStdout(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"screen", "resolution"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			sc := &pftest.Screenshotter{Width: 8, Height: 8}
			return pftest.New(sc, nil, nil, nil), nil
		}
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got, want := stdout, "8x8\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunWithFactoryReportsParseError(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"screen", "grab", "--rect", "bad", "--out", "/tmp/pf-test.png"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			return pftest.New(&pftest.Screenshotter{Width: 8, Height: 8}, nil, nil, nil), nil
		}
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `--rect must be x0,y0,x1,y1; got "bad"`) {
		t.Fatalf("stderr = %q, want rect parse error", stderr)
	}
}

func TestWindowCommands(t *testing.T) {
	t.Run("list plain", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42, Active: true},
			{ID: 8, Title: "Terminal", AppID: "org.gnome.Terminal", PID: 99},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "list", "--output", "plain"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "0x7\tFirefox") || !strings.Contains(stdout, "app_id=firefox") {
			t.Fatalf("stdout = %q, want plain window rows", stdout)
		}
	})

	t.Run("list json", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42, Active: true},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "list", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got []window.Info
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		if len(got) != 1 || got[0].Title != "Firefox" || got[0].ID != 7 {
			t.Fatalf("decoded windows = %+v, want Firefox ID 7", got)
		}
	})

	t.Run("find", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{{
			{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42},
			{ID: 8, Title: "Firefox Settings", AppID: "firefox", PID: 43},
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "find", "app_id:firefox", "state:-minimized"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "Firefox") || !strings.Contains(stdout, "Firefox Settings") {
			t.Fatalf("stdout = %q, want matching windows", stdout)
		}
	})

	t.Run("wait-for", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{},
			{{ID: 11, Title: "Firefox", AppID: "firefox", PID: 42}},
		}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "wait-for", "title:Firefox", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "Firefox") {
			t.Fatalf("stdout = %q, want matching window", stdout)
		}
	})

	t.Run("wait-close", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{{ID: 11, Title: "Firefox", AppID: "firefox", PID: 42}},
			{},
		}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "wait-close", "title:Firefox", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})

	t.Run("wait-close zero poll", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{{ID: 11, Title: "Firefox", AppID: "firefox", PID: 42}},
			{},
		}}
		stdout, stderr, code := captureRunIO(t, []string{"window", "wait-close", "title:Firefox", "--poll", "0s", "--timeout", "250ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

func TestScreenAndInputCommands(t *testing.T) {
	t.Run("get-multiple-pixels", func(t *testing.T) {
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 4})
		img.Set(1, 1, color.RGBA{R: 10, G: 20, B: 30, A: 255})

		stdout, stderr, code := captureRunIO(t, []string{"screen", "get-multiple-pixels", "--points", "0,0;1,1", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{img}}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got []struct {
			X int   `json:"x"`
			Y int   `json:"y"`
			R uint8 `json:"r"`
			G uint8 `json:"g"`
			B uint8 `json:"b"`
			A uint8 `json:"a"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		if len(got) != 2 || got[0].X != 0 || got[0].R != 1 || got[1].X != 1 || got[1].G != 20 {
			t.Fatalf("unexpected samples: %+v", got)
		}
	})

	t.Run("wait-for-fn", func(t *testing.T) {
		sc := &pftest.Screenshotter{Frames: []image.Image{
			pftest.SolidImage(2, 2, color.RGBA{}),
			pftest.SolidImage(2, 2, color.RGBA{R: 255, A: 255}),
		}}
		stdout, stderr, code := captureRunIO(t, []string{"screen", "wait-for-fn", "--rect", "0,0,2,2", "--predicate", "non-empty", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(sc, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(stdout), 16, 32); err != nil {
			t.Fatalf("stdout = %q, want 8-digit hex hash: %v", stdout, err)
		}
	})

	t.Run("scan-for empty", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"find", "scan-for", "--rects", "", "--wants", ""}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Width: 2, Height: 2}, nil, nil, nil), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "scan-for requires at least one rect/hash pair") {
			t.Fatalf("stderr = %q, want empty-input error", stderr)
		}
	})

	t.Run("wait-for-settle", func(t *testing.T) {
		sc := &pftest.Screenshotter{Frames: []image.Image{
			pftest.SolidImage(2, 2, color.RGBA{}),
			pftest.SolidImage(2, 2, color.RGBA{G: 255, A: 255}),
			pftest.SolidImage(2, 2, color.RGBA{G: 255, A: 255}),
		}}
		stdout, stderr, code := captureRunIO(t, []string{"screen", "wait-for-settle", "--rect", "0,0,2,2", "--stable", "2", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(sc, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(stdout), 16, 32); err != nil {
			t.Fatalf("stdout = %q, want 8-digit hex hash: %v", stdout, err)
		}
	})

	t.Run("input sync", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"input", "sync"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, &pftest.Inputter{}, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
	})
}

func TestScreenHashAndWatch(t *testing.T) {
	t.Run("hash", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"screen", "hash", "--rect", "0,0,2,2"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				img := pftest.SolidImage(2, 2, color.RGBA{R: 1, G: 2, B: 3, A: 255})
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{img}}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if _, err := strconv.ParseUint(strings.TrimSpace(stdout), 16, 32); err != nil {
			t.Fatalf("stdout = %q, want 8-digit hex hash: %v", stdout, err)
		}
	})

	t.Run("watch json", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms", "--duration", "20ms", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				img := pftest.SolidImage(2, 2, color.RGBA{R: 4, G: 5, B: 6, A: 255})
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{img}}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, `"event":"initial"`) {
			t.Fatalf("stdout = %q, want initial JSON event", stdout)
		}
	})
}

func TestFindWaitForVisibleChange(t *testing.T) {
	initial := pftest.SolidImage(2, 2, color.RGBA{A: 255})
	changed := pftest.SolidImage(2, 2, color.RGBA{R: 200, G: 20, B: 10, A: 255})
	stdout, stderr, code := captureRunIO(t, []string{"find", "wait-for-visible-change", "--rect", "0,0,2,2", "--poll", "1ms", "--timeout", "50ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			return pftest.New(&pftest.Screenshotter{Frames: []image.Image{initial, changed}}, nil, nil, nil), nil
		}
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if _, err := strconv.ParseUint(strings.TrimSpace(stdout), 16, 32); err != nil {
		t.Fatalf("stdout = %q, want 8-digit hex hash: %v", stdout, err)
	}
}

func TestWindowGeometryAndVisibility(t *testing.T) {
	mgr := &pftest.Manager{Lists: [][]window.Info{{{ID: 7, Title: "Firefox", AppID: "firefox", X: 10, Y: 20, W: 100, H: 200}}}}

	t.Run("geometry", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "get-geometry", "Firefox"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if got, want := strings.TrimSpace(stdout), "10,20,110,220"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("visible", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "is-visible", "Firefox"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if got, want := strings.TrimSpace(stdout), "true"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("not-found", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "is-visible", "Terminal"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if got, want := strings.TrimSpace(stdout), "false"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	})

	t.Run("backend error", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "is-visible", "Firefox"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, &pftest.Manager{Err: errors.New("boom")}, nil), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "boom") {
			t.Fatalf("stderr = %q, want backend error", stderr)
		}
	})
}

type fakeOutputLister struct {
	infos []output.Info
}

func (f *fakeOutputLister) List(ctx context.Context) ([]output.Info, error) {
	return f.infos, nil
}

func (f *fakeOutputLister) Close() error { return nil }

func TestOutputListInfoAndRun(t *testing.T) {
	t.Run("output list json", func(t *testing.T) {
		fake := &fakeOutputLister{infos: []output.Info{{
			Name:        "HDMI-A-1",
			Backend:     "wayland",
			Geometry:    output.Geometry{X: 0, Y: 0, W: 1920, H: 1080},
			ResolutionW: 1920,
			ResolutionH: 1080,
			Scale:       2,
		}}}
		stdout, stderr, code := captureRunIO(t, []string{"output", "list", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return &perfuncted.Perfuncted{Output: perfuncted.OutputBundle{Lister: fake}}, nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got output.Info
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		if got.Name != "HDMI-A-1" || got.Geometry.W != 1920 || got.Scale != 2 {
			t.Fatalf("decoded output = %+v", got)
		}
	})

	t.Run("info json", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"info", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return nil, nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		for _, key := range []string{"compositor", "environment", "probes", "capabilities"} {
			if _, ok := got[key]; !ok {
				t.Fatalf("missing key %q in info report: %+v", key, got)
			}
		}
	})

	t.Run("run script", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "script.pf")
		if err := os.WriteFile(script, []byte("# comment\ninfo\n"), 0600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code := captureRunIO(t, []string{"run", script}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return &perfuncted.Perfuncted{}, nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("json.Unmarshal(stdout) error = %v; stdout=%q", err, stdout)
		}
		if _, ok := got["capabilities"]; !ok {
			t.Fatalf("stdout = %q, want info report", stdout)
		}
	})
}

func captureRunIO(t *testing.T, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) (string, string, int) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr

	rOut, wOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rErr, wErr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = wOut
	os.Stderr = wErr
	code := runWithFactory(context.Background(), args, openPFFactory)

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	outBytes, err := io.ReadAll(rOut)
	if err != nil {
		t.Fatal(err)
	}
	errBytes, err := io.ReadAll(rErr)
	if err != nil {
		t.Fatal(err)
	}

	return string(outBytes), string(errBytes), code
}

func TestVersionCmd(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"version"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
		return func() (*perfuncted.Perfuncted, error) {
			return nil, nil
		}
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.HasPrefix(stdout, "pf ") {
		t.Fatalf("stdout = %q, want it to start with 'pf '", stdout)
	}
	if strings.Contains(stdout, "dev") {
		t.Fatalf("stdout = %q, want it to avoid hardcoded dev", stdout)
	}
}
