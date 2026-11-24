package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestScreenGrabSelectsWindowByStableIDTitleAndPID(t *testing.T) {
	first := window.Info{NativeID: "editor-one", Title: "Editor", PID: 41, X: -5, Y: 10, W: 30, H: 20}
	second := window.Info{NativeID: "editor-two", Title: "Editor", PID: 42, X: 50, Y: 60, W: 20, H: 15}
	for _, tc := range []struct {
		name     string
		args     []string
		windows  []window.Info
		wantRect image.Rectangle
	}{
		{name: "stable id disambiguates duplicate title", args: []string{"grab", "--window-id", "editor-two", "--out", "-"}, windows: []window.Info{first, second}, wantRect: image.Rect(50, 60, 70, 75)},
		{name: "exact title", args: []string{"grab", "--window-title", "Editor", "--out", "-"}, windows: []window.Info{first}, wantRect: image.Rect(-5, 10, 25, 30)},
		{name: "pid", args: []string{"grab", "--pid", "42", "--out", "-"}, windows: []window.Info{first, second}, wantRect: image.Rect(50, 60, 70, 75)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows := tc.windows
			mgr := &pftest.Manager{Lists: [][]window.Info{windows, windows, windows, windows}}
			var rect image.Rectangle
			grabs := 0
			sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
				rect = requested
				grabs++
				return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
			}}
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				return pftest.New(sc, nil, mgr, nil), nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(tc.args)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%q)", err, stderr.String())
			}
			if grabs != 1 {
				t.Fatalf("captures=%d, want one", grabs)
			}
			if rect != tc.wantRect {
				t.Fatalf("capture rect=%v, want %v", rect, tc.wantRect)
			}
			if len(mgr.Activated) != 0 {
				t.Fatalf("activated windows=%v", mgr.Activated)
			}
			if !bytes.HasPrefix(stdout.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("stdout is not a PNG: %q", stdout.Bytes()[:min(stdout.Len(), 32)])
			}
			if _, err := png.Decode(bytes.NewReader(stdout.Bytes())); err != nil {
				t.Fatalf("decode stdout PNG: %v", err)
			}
			if !strings.Contains(stderr.String(), "mode=visible-screen-crop") || !strings.Contains(stderr.String(), "screen_rect=") {
				t.Fatalf("missing separate capture report: %q", stderr.String())
			}
		})
	}
}

func TestScreenGrabWindowWritesPNGAndSeparateMetadata(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "window.png")
	metadataPath := filepath.Join(dir, "window.json")
	info := window.Info{NativeID: "editor", Title: "Editor", AppID: "org.example.Editor", PID: 42, X: 10, Y: 20, W: 24, H: 12}
	windows := []window.Info{info}
	mgr := &pftest.Manager{Lists: [][]window.Info{windows, windows, windows}}
	sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
	}}
	command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(sc, nil, mgr, nil), nil
	})
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"grab", "--window-title", "Editor", "--out", imagePath, "--metadata", metadataPath})
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%q)", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != imagePath {
		t.Fatalf("stdout=%q, want image path", stdout.String())
	}
	file, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(file); err != nil {
		_ = file.Close()
		t.Fatalf("published invalid PNG: %v", err)
	}
	_ = file.Close()
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata perfuncted.WindowCapture
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.WindowID != "editor" || metadata.ScreenRect != image.Rect(10, 20, 34, 32) || metadata.Mode != "visible-screen-crop" {
		t.Fatalf("metadata=%+v", metadata)
	}
	if metadata.PixelBounds != image.Rect(0, 0, 24, 12) || metadata.PixelScaleX != 1 || metadata.PixelScaleY != 1 {
		t.Fatalf("pixel provenance=%+v", metadata)
	}
	if metadata.Occlusion == "" || metadata.GeometrySource != "window-manager-reported" {
		t.Fatalf("incomplete provenance metadata=%+v", metadata)
	}
}

func TestScreenGrabWindowRejectsAmbiguityAndSelectorConflictsBeforeCapture(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		list [][]window.Info
	}{
		{name: "duplicate exact title", args: []string{"grab", "--window-title", "Editor", "--out", "-"}, list: [][]window.Info{{
			{NativeID: "one", Title: "Editor", X: 1, Y: 2, W: 5, H: 5},
			{NativeID: "two", Title: "Editor", X: 8, Y: 2, W: 5, H: 5},
		}}},
		{name: "PID with multiple windows", args: []string{"grab", "--pid", "42", "--out", "-"}, list: [][]window.Info{{
			{NativeID: "one", Title: "Editor", PID: 42, X: 1, Y: 2, W: 5, H: 5},
			{NativeID: "two", Title: "Dialog", PID: 42, X: 8, Y: 2, W: 5, H: 5},
		}}},
		{name: "multiple selectors", args: []string{"grab", "--window-id", "one", "--pid", "42"}},
		{name: "window and rect", args: []string{"grab", "--window-id", "one", "--rect", "1,2,6,7"}},
		{name: "metadata without selector", args: []string{"grab", "--metadata", "capture.json"}},
		{name: "metadata stdout", args: []string{"grab", "--window-id", "one", "--metadata", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens, grabs := 0, 0
			mgr := &pftest.Manager{Lists: tc.list}
			sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
				grabs++
				return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
			}}
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(sc, nil, mgr, nil), nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(tc.args)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			err := command.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("invalid or ambiguous selector was accepted")
			}
			if tc.name != "duplicate exact title" && tc.name != "PID with multiple windows" && opens != 0 {
				t.Fatalf("opened session %d times before selector validation", opens)
			}
			if grabs != 0 {
				t.Fatalf("captured %d times for rejected selector", grabs)
			}
			if len(mgr.Activated) != 0 {
				t.Fatalf("activated windows=%v", mgr.Activated)
			}
		})
	}
}

func TestScreenGrabMetadataCannotOverwritePNG(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(imagePath, []byte("existing image"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadataAlias := filepath.Join(dir, "metadata.json")
	if err := os.Symlink(imagePath, metadataAlias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		imagePath    string
		metadataPath string
	}{
		{name: "same lexical path", imagePath: "same.png", metadataPath: "./same.png"},
		{name: "same file through symlink", imagePath: imagePath, metadataPath: metadataAlias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return nil, nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs([]string{"grab", "--window-id", "id", "--out", tc.imagePath, "--metadata", tc.metadataPath})
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			if err := command.ExecuteContext(context.Background()); err == nil {
				t.Fatal("same PNG and metadata path was accepted")
			}
			if opens != 0 {
				t.Fatalf("opened session %d times before path-conflict validation", opens)
			}
		})
	}
}
