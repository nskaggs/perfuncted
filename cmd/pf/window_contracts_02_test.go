package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWindowMatchArgsPreservesMultiwordCLISelection(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"title=Project Notes", "app_id=org.example.Editor", "visible", "active=false"})
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleExact != "Project Notes" || got.AppID != "org.example.Editor" || !got.VisibleOnly || got.Active == nil || *got.Active {
		t.Fatalf("window selection lost CLI fields: %+v", got)
	}
	empty, err := parseWindowMatchArgs(nil)
	if err != nil || empty.TitleExact != "" || empty.Active != nil || empty.VisibleOnly {
		t.Fatalf("empty match: %+v %v", empty, err)
	}
}

func TestParseWindowMatchArgsRejectsInvalidPIDWithoutMatchingEverything(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"pid=not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "parse pid") {
		t.Fatalf("malformed PID accepted: %+v %v", got, err)
	}
	if got.PID != nil || got.TitleContains != "" || got.TitleExact != "" {
		t.Fatalf("error returned partial match: %+v", got)
	}
}

func TestParseWindowMatchArgsPreservesQuotedPositionalTitle(t *testing.T) {
	got, err := parseWindowMatchArgs([]string{"Quarterly Planning", "active"})
	if err != nil {
		t.Fatalf("quoted positional title should be one token: %v", err)
	}
	if got.TitleContains != "Quarterly Planning" || got.Active == nil || !*got.Active {
		t.Fatalf("quoted title match altered: %+v", got)
	}
}

func TestCLIStableIDLeadingZeroDecimalDoesNotSelectWrongWindow(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "8", ID: 8, Title: "wrong"}, {NativeID: "10", ID: 10, Title: "correct"}}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.CachedInfo().ID; id != 10 {
		t.Fatalf("decimal ID 010 selected window %d, want 10", id)
	}
}

func TestCLINumericHashLeadingZeroDecimal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint32
	}{{"010", 10}, {"08", 8}, {"0x10", 16}, {"abcdef", 0xabcdef}} {
		got, err := parseHash(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("parseHash(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
		}
	}
}

func TestCLIStableIDPrefersExactOpaqueHandleOverNumericAlias(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{
		{NativeID: "10", ID: 10, Title: "numeric alias"},
		{NativeID: "010", Title: "exact opaque handle"},
	}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	target, err := findWindowByStableID(context.Background(), pf.Windows, "010")
	if err != nil {
		t.Fatal(err)
	}
	if id := target.ID().String(); id != "010" {
		t.Fatalf("selected native ID %q, want exact 010", id)
	}
}

// Run actual Cobra leaf commands against a deliberately denied synthetic
// desktop. No operation is allowed to report success or mask an authorization
// failure with a secondary backend error.
func TestWindowAndScreenCommandsPropagateDesktopAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic desktop authorization denied")
	cases := []struct {
		name string
		args []string
	}{
		{"window list", []string{"window", "list"}},
		{"window list JSON", []string{"window", "list", "--output", "json"}},
		{"window activate", []string{"window", "activate", "Editor"}},
		{"window activate by id", []string{"window", "activate-id", "window-id"}},
		{"window active title", []string{"window", "active"}},
		{"window move", []string{"window", "move", "--title", "Editor", "--x", "11", "--y", "20"}},
		{"window resize", []string{"window", "resize", "--title", "Editor", "--w", "300", "--h", "200"}},
		{"window fullscreen", []string{"window", "fullscreen", "Editor"}},
		{"window unfullscreen", []string{"window", "unfullscreen", "Editor"}},
		{"window find", []string{"window", "find", "title=Editor"}},
		{"window wait", []string{"window", "wait-for", "title=Editor"}},
		{"window wait close", []string{"window", "wait-close", "title=Editor"}},
		{"window geometry", []string{"window", "get-geometry", "Editor"}},
		{"window by title", []string{"window", "find-by-title", "Editor"}},
		{"window visible", []string{"window", "is-visible", "Editor"}},
		{"window watch", []string{"window", "watch"}},
		{"window close", []string{"window", "close", "Editor"}},
		{"window close id", []string{"window", "close-id", "window-id"}},
		{"window minimize", []string{"window", "minimize", "Editor"}},
		{"screen grab", []string{"screen", "grab", "--rect", "0,0,2,2"}},
		{"screen hash", []string{"screen", "hash", "--rect", "0,0,2,2"}},
		{"screen pixel", []string{"screen", "pixel", "--x", "1", "--y", "1"}},
		{"screen all pixels", []string{"screen", "get-all-pixels"}},
		{"screen grab region", []string{"screen", "grab-region", "--rect", "0,0,2,2"}},
		{"screen watch", []string{"screen", "watch", "--duration", "1ms"}},
		{"screen resolution", []string{"screen", "resolution"}},
		{"screen wait predicate", []string{"screen", "wait-for-fn", "--timeout", "1ms"}},
		{"screen wait settle", []string{"screen", "wait-for-settle", "--timeout", "1ms"}},
		{"screen pixel batch", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, denied }
			})
			var stdout, stderr bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("denial not propagated for %v: err=%v stdout=%q stderr=%q", tc.args, err, stdout.String(), stderr.String())
			}
			if opened != 1 {
				t.Fatalf("expected exactly one session authorization for %v, got %d", tc.args, opened)
			}
			if stdout.Len() != 0 {
				t.Fatalf("denied command published partial success: %q", stdout.String())
			}
		})
	}
}

func TestWindowWaitForZeroPollUsesSafeDefaultAndFindMissingRemainsError(t *testing.T) {
	backend := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Editor", X: 1, Y: 2, W: 100, H: 80}}}}
	for _, tc := range []struct {
		name      string
		args      []string
		want      string
		wantError bool
	}{
		{name: "zero interval", args: []string{"wait-for", "title=Editor", "--poll=0s", "--timeout=200ms"}, want: "Editor"},
		{name: "find missing", args: []string{"find-by-title", "Missing"}, want: "not found", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, backend, nil), nil }, nil)
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetArgs(tc.args)
			var stdout bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(io.Discard)
			err := cmd.ExecuteContext(context.Background())
			if tc.wantError {
				if !errors.Is(err, window.ErrWindowNotFound) || stdout.Len() != 0 {
					t.Fatalf("missing match: err=%v stdout=%q", err, stdout.String())
				}
			} else if err != nil || !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("zero interval wait: err=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestScreenWatchZeroPollUsesBoundedDefault(t *testing.T) {
	sc := &pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(2, 2, color.RGBA{A: 255})}}
	opens := 0
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return pftest.New(sc, nil, nil, nil), nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"watch", "--rect=0,0,2,2", "--poll=0s", "--duration=15ms", "--output=json"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if opens != 1 || !strings.Contains(stdout.String(), `"event":"initial"`) {
		t.Fatalf("zero poll watch: opens=%d output=%q", opens, stdout.String())
	}
}

func TestScreenshotTerminalPreflightPropagatesClosedFileStatError(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "closed-screenshot-output"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := refuseBinaryTerminal(file); err == nil || !strings.Contains(err.Error(), "inspect screenshot output") {
		t.Fatalf("closed screenshot file accepted: %v", err)
	}
}

// An activation event is not an acknowledgement of focus. Only an observed
// transition of the selected stable window may authorize a verified success.
func TestWindowCLIReportsVerifiedFocusOnlyAfterSelectedWindowBecomesActive(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"title", []string{"window", "activate", "Editor", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}},
		{"stable ID", []string{"window", "activate-id", "editor", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inactive := window.Info{NativeID: "editor", Title: "Editor", Active: false}
			active := inactive
			active.Active = true
			mgr := &pftest.Manager{Lists: [][]window.Info{{inactive}, {inactive}, {active}}}
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(nil, nil, mgr, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, "activation verified: ") || len(mgr.Activated) != 1 || mgr.Activated[0] != "editor" {
				t.Fatalf("selected window focus not verified: code=%d stdout=%q stderr=%q activated=%q", code, out, stderr, mgr.Activated)
			}
		})
	}
}

// A close request alone is insufficient: observe the stable target disappear
// from an independently refreshed window listing before reporting success.
func TestWindowCLIReportsVerifiedCloseOnlyAfterTargetDisappears(t *testing.T) {
	info := window.Info{NativeID: "dialog", Title: "Save dialog"}
	mgr := &pftest.Manager{Lists: [][]window.Info{{info}, {info}, {}}}
	out, stderr, code := captureRunIO(t, []string{"window", "close-id", "dialog", "--verify", "--verify-timeout=250ms", "--verify-poll=1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, nil, mgr, nil), nil
		}
	})
	if code != 0 || !strings.Contains(out, "close verified: dialog") {
		t.Fatalf("disappearance verification not acknowledged: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
