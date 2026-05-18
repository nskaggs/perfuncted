package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
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
		{"screen", "wait-for-no-change-from"},
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
		img := image.NewRGBA(image.Rect(10, 20, 13, 23))
		img.Set(10, 20, color.RGBA{R: 1, G: 2, B: 3, A: 4})
		img.Set(12, 22, color.RGBA{R: 10, G: 20, B: 30, A: 255})

		stdout, stderr, code := captureRunIO(t, []string{"screen", "get-multiple-pixels", "--points", "10,20;12,22", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{img}, ZeroOrigin: true}, nil, nil, nil), nil
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
		if len(got) != 2 || got[0].X != 10 || got[0].Y != 20 || got[0].R != 1 || got[1].X != 12 || got[1].Y != 22 || got[1].G != 20 {
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

	t.Run("wait-for-no-change-from hex", func(t *testing.T) {
		frame := pftest.SolidImage(2, 2, color.RGBA{B: 255, A: 255})
		want := find.PixelHash(frame, nil)
		sc := &pftest.Screenshotter{Frames: []image.Image{frame}}
		stdout, stderr, code := captureRunIO(t, []string{"screen", "wait-for-no-change-from", "--rect", "0,0,2,2", "--initial", "0x" + strconv.FormatUint(uint64(want), 16), "--stable", "1", "--poll", "1ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
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

	t.Run("watch json stable", func(t *testing.T) {
		imgA := pftest.SolidImage(2, 2, color.RGBA{R: 4, G: 5, B: 6, A: 255})
		imgB := pftest.SolidImage(2, 2, color.RGBA{R: 9, G: 8, B: 7, A: 255})
		stdout, stderr, code := captureRunIO(t, []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms", "--duration", "20ms", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{imgA, imgA, imgB}}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, `"event":"initial"`) || !strings.Contains(stdout, `"stable":2`) {
			t.Fatalf("stdout = %q, want initial JSON event and stable count 2", stdout)
		}
	})

	t.Run("watch canceled via context", func(t *testing.T) {
		img := pftest.SolidImage(2, 2, color.RGBA{R: 4, G: 5, B: 6, A: 255})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		stdout, stderr, code := captureRunIOContext(t, ctx, []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
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

	t.Run("watch invalid output", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"screen", "watch", "--rect", "0,0,2,2", "--output", "bogus"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(2, 2, color.RGBA{A: 255})}}, nil, nil, nil), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "invalid --output") {
			t.Fatalf("stderr = %q, want invalid output error", stderr)
		}
	})
}

func TestScreenAndInputCliOnlyFeatures(t *testing.T) {
	t.Run("grab-region png", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(10, 20, 14, 24))
		want := color.RGBA{R: 11, G: 22, B: 33, A: 255}
		frame.SetRGBA(12, 22, want)
		stdout, stderr, code := captureRunIO(t, []string{"screen", "grab-region", "--rect", "11,21,13,23", "--out", "-"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{frame}, ZeroOrigin: true}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		img, err := png.Decode(bytes.NewReader([]byte(stdout)))
		if err != nil {
			t.Fatalf("png.Decode stdout: %v", err)
		}
		if got, wantBounds := img.Bounds(), image.Rect(0, 0, 2, 2); got != wantBounds {
			t.Fatalf("bounds = %v, want %v", got, wantBounds)
		}
		if got := color.RGBAModel.Convert(img.At(1, 1)).(color.RGBA); got != want {
			t.Fatalf("pixel = %+v, want %+v", got, want)
		}
	})

	t.Run("get-all-pixels raw", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 2, 1))
		frame.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 4})
		frame.SetRGBA(1, 0, color.RGBA{R: 5, G: 6, B: 7, A: 8})
		stdout, stderr, code := captureRunIO(t, []string{"screen", "get-all-pixels"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(&pftest.Screenshotter{Frames: []image.Image{frame}}, nil, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if got, want := []byte(stdout), frame.Pix; !bytes.Equal(got, want) {
			t.Fatalf("raw pixels = %v, want %v", got, want)
		}
	})

	t.Run("input type stdin", func(t *testing.T) {
		inp := &pftest.Inputter{}
		stdout, stderr, code := captureRunIOWithStdin(t, "hello\n", []string{"input", "type", "--stdin"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, inp, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if got := inp.Typed(); got != "hello\n" {
			t.Fatalf("typed text = %q, want %q", got, "hello\n")
		}
	})

	t.Run("click repeat", func(t *testing.T) {
		inp := &pftest.Inputter{}
		stdout, stderr, code := captureRunIO(t, []string{"input", "click", "--x", "7", "--y", "9", "--button", "2", "--repeat", "3", "--delay", "1ms"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, inp, nil, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if got := len(inp.Calls); got != 3 {
			t.Fatalf("click calls = %d, want 3; calls=%v", got, inp.Calls)
		}
		if !strings.Contains(stdout, "clicked button 2 at 7,9") {
			t.Fatalf("stdout = %q, want click confirmation", stdout)
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

func TestWindowWatchAndOutputValidation(t *testing.T) {
	t.Run("watch json", func(t *testing.T) {
		mgr := &pftest.Manager{Lists: [][]window.Info{
			{{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42}},
			{{ID: 7, Title: "Firefox", AppID: "firefox", PID: 42}, {ID: 8, Title: "Terminal", AppID: "org.gnome.Terminal", PID: 99}},
		}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stdout, stderr, code := captureRunIOContext(t, ctx, []string{"window", "watch", "--output", "json"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, `"count":1`) || !strings.Contains(stdout, `"count":2`) {
			t.Fatalf("stdout = %q, want both window counts", stdout)
		}
	})

	t.Run("watch invalid output", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "watch", "--output", "bogus"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, &pftest.Manager{Lists: [][]window.Info{{}}}, nil), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "invalid --output") {
			t.Fatalf("stderr = %q, want invalid output error", stderr)
		}
	})

	t.Run("geometry invalid output", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"window", "get-geometry", "Firefox", "--output", "bogus"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				mgr := &pftest.Manager{Lists: [][]window.Info{{{ID: 7, Title: "Firefox", AppID: "firefox", X: 10, Y: 20, W: 100, H: 200}}}}
				return pftest.New(nil, nil, mgr, nil), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "invalid --output") {
			t.Fatalf("stderr = %q, want invalid output error", stderr)
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

	t.Run("output list invalid output", func(t *testing.T) {
		fake := &fakeOutputLister{infos: []output.Info{{Name: "HDMI-A-1", Backend: "wayland"}}}
		stdout, stderr, code := captureRunIO(t, []string{"output", "list", "--output", "bogus"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return &perfuncted.Perfuncted{Output: perfuncted.OutputBundle{Lister: fake}}, nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "unknown output format") {
			t.Fatalf("stderr = %q, want unknown output format", stderr)
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

func TestClipboardCommands(t *testing.T) {
	t.Run("clipboard get", func(t *testing.T) {
		cb := &pftest.Clipboard{Text: "clipboard text"}
		stdout, stderr, code := captureRunIO(t, []string{"clipboard", "get"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, nil, cb), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "clipboard text" {
			t.Fatalf("stdout = %q, want clipboard text", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("clipboard set", func(t *testing.T) {
		cb := &pftest.Clipboard{}
		stdout, stderr, code := captureRunIO(t, []string{"clipboard", "set", "new text"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, nil, cb), nil
			}
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if stdout != "" || stderr != "" {
			t.Fatalf("stdout=%q stderr=%q, want both empty", stdout, stderr)
		}
		if cb.Text != "new text" {
			t.Fatalf("clipboard text = %q, want new text", cb.Text)
		}
	})

	t.Run("clipboard set requires argument", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"clipboard", "set"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) {
				return pftest.New(nil, nil, nil, &pftest.Clipboard{}), nil
			}
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "accepts 1 arg") {
			t.Fatalf("stderr = %q, want argument count error", stderr)
		}
	})
}

func TestInfoSessionAndDocsCommands(t *testing.T) {
	t.Run("info invalid output", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"info", "--output", "bogus"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) { return nil, nil }
		})
		if code == 0 {
			t.Fatalf("exit code = 0, want non-zero; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, "invalid --output") {
			t.Fatalf("stderr = %q, want invalid output error", stderr)
		}
	})

	t.Run("session type", func(t *testing.T) {
		stdout, stderr, code := captureRunIO(t, []string{"session", "type"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) { return nil, nil }
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if !strings.Contains(stdout, "session:") {
			t.Fatalf("stdout = %q, want session summary", stdout)
		}
	})

	t.Run("session check", func(t *testing.T) {
		runtimeDir := t.TempDir()
		socketPath := filepath.Join(runtimeDir, "wayland-1")
		ln, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
		t.Setenv("WAYLAND_DISPLAY", "wayland-1")
		t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/dbus-test")

		stdout, stderr, code := captureRunIO(t, []string{"session", "check"}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) { return nil, nil }
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		for _, want := range []string{
			"XDG_RUNTIME_DIR=",
			"WAYLAND_DISPLAY=wayland-1",
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/dbus-test",
		} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("stdout = %q, want %q", stdout, want)
			}
		}
	})

	t.Run("docs", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr, code := captureRunIO(t, []string{"docs", "--dir", dir}, func(*cliConfig) func() (*perfuncted.Perfuncted, error) {
			return func() (*perfuncted.Perfuncted, error) { return nil, nil }
		})
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "pf.md")); err != nil {
			t.Fatalf("docs were not generated in %s: %v", dir, err)
		}
		if !strings.Contains(stdout, "Documentation generated") {
			t.Fatalf("stdout = %q, want generation summary", stdout)
		}
	})
}

func captureRunIO(t *testing.T, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) (string, string, int) {
	t.Helper()
	return captureRunIOContext(t, context.Background(), args, openPFFactory)
}

func captureRunIOContext(t *testing.T, ctx context.Context, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) (string, string, int) {
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
	code := runWithFactory(ctx, args, openPFFactory)

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

func captureRunIOWithStdin(t *testing.T, stdin string, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) (string, string, int) {
	t.Helper()

	oldStdin := os.Stdin
	rIn, wIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(wIn, stdin); err != nil {
		t.Fatal(err)
	}
	if err := wIn.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = rIn
	defer func() {
		os.Stdin = oldStdin
		rIn.Close()
	}()

	return captureRunIOContext(t, context.Background(), args, openPFFactory)
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
