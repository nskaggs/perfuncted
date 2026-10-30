package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestSyntheticX11ClipboardTransport(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "xclip")
	selection := filepath.Join(dir, "selection")
	script := "#!/bin/sh\ncase \"$*\" in *-o*) cat \"$PF_SYNTHETIC_CLIP_FILE\";; *) cat > \"$PF_SYNTHETIC_CLIP_FILE\";; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	rt := env.FromEnviron([]string{"DISPLAY=:234", "PF_SYNTHETIC_CLIP_FILE=" + selection, "PATH=/bin:/usr/bin"})
	cb, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("open x11 fixture: %v", err)
	}
	defer cb.Close()
	if err := cb.Set(context.Background(), "new\nclipboard"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, err := cb.Get(context.Background()); err != nil || got != "new\nclipboard" {
		t.Fatalf("get=%q err=%v", got, err)
	}
	if file, err := os.ReadFile(selection); err != nil || string(file) != "new\nclipboard" {
		t.Fatalf("written fixture %q err %v", file, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cb.Get(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled get %v", err)
	}
	if err := cb.Set(ctx, "must not write"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled set %v", err)
	}
	if file, _ := os.ReadFile(selection); string(file) != "new\nclipboard" {
		t.Fatalf("canceled set mutated selection: %q", file)
	}
}
func TestClipboardOpenRuntimeUnreachableAndNoTools(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, vars := range [][]string{{}, {"DISPLAY=:234"}, {"WAYLAND_DISPLAY=missing-wayland", "XDG_RUNTIME_DIR=" + t.TempDir()}} {
		rt := env.FromEnviron(vars)
		if cb, err := OpenRuntime(rt); cb != nil || !errors.Is(err, ErrNoClipboardTool) {
			t.Fatalf("unexpected clipboard for %v: %T %v", vars, cb, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if cb, err := OpenRuntimeContext(ctx, env.FromEnviron(nil)); cb != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open %T %v", cb, err)
	}
	if !strings.Contains(ErrNoClipboardTool.Error(), "clipboard") {
		t.Fatal("error lacks capability guidance")
	}
}
