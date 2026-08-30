package perfuncted_test

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestCaptureFailureBundleWritesIndependentArtifacts(t *testing.T) {
	screenshotter := &pftest.Screenshotter{
		Frames: []image.Image{pftest.SolidImage(4, 3, color.RGBA{R: 10, G: 20, B: 30, A: 255})},
	}
	manager := &pftest.Manager{
		Lists:  [][]window.Info{{{NativeID: "window-1", Title: "Editor", PID: 42, Active: true}}},
		Titles: []string{"Editor"},
	}
	outputs := &testOutputLister{infos: []output.Info{{
		Name:      "DP-1",
		Backend:   "wayland",
		Geometry:  output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080},
		Scale:     1,
		Available: true,
	}}}
	session := pftest.NewWithOutputs(screenshotter, &pftest.Inputter{}, manager, outputs, nil)
	defer session.Close()
	if err := session.Input.Type(context.Background(), "{ctrl+s}"); err != nil {
		t.Fatalf("trace setup: %v", err)
	}

	directory := filepath.Join(t.TempDir(), "bundle")
	path, err := session.CaptureFailureBundle(context.Background(), perfuncted.FailureBundleOptions{
		Directory: directory,
		Operation: "save document",
		Error:     errors.New("document did not appear"),
		Metadata:  map[string]string{"case": "multi-output"},
	})
	if err != nil {
		t.Fatalf("CaptureFailureBundle: %v", err)
	}
	if path != directory {
		t.Fatalf("bundle path = %q, want %q", path, directory)
	}

	manifest := readJSONMap(t, filepath.Join(directory, "manifest.json"))
	if manifest["operation"] != "save document" || manifest["error"] != "document did not appear" {
		t.Fatalf("manifest context = %v", manifest)
	}
	artifacts, ok := manifest["artifacts"].([]any)
	if !ok || len(artifacts) != 5 {
		t.Fatalf("manifest artifacts = %v, want five artifacts", manifest["artifacts"])
	}
	trace, traceErr := os.ReadFile(filepath.Join(directory, "trace.txt"))
	if traceErr != nil || !strings.Contains(string(trace), "input type") {
		t.Fatalf("trace artifact = %q, %v", trace, traceErr)
	}
	imageFile, err := os.Open(filepath.Join(directory, "screenshot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, decodeErr := png.Decode(imageFile); decodeErr != nil {
		t.Fatalf("screenshot.png is not a PNG: %v", decodeErr)
	}
	_ = imageFile.Close()

	var outputInfos []output.Info
	outputData, err := os.ReadFile(filepath.Join(directory, "outputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(outputData, &outputInfos); err != nil || len(outputInfos) != 1 {
		t.Fatalf("outputs.json = %s, err=%v", outputData, err)
	}
}

func TestCaptureFailureBundleKeepsSuccessfulArtifactsWhenCapabilityFails(t *testing.T) {
	session := pftest.New(
		&pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(1, 1, color.RGBA{A: 255})}},
		nil,
		nil,
		nil,
	)
	defer session.Close()

	directory := filepath.Join(t.TempDir(), "bundle")
	path, err := session.CaptureFailureBundle(context.Background(), perfuncted.FailureBundleOptions{Directory: directory})
	if err == nil {
		t.Fatal("CaptureFailureBundle error = nil, want unavailable-artifact error")
	}
	if path != directory {
		t.Fatalf("bundle path = %q, want %q", path, directory)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "screenshot.png")); statErr != nil {
		t.Fatalf("successful screenshot artifact missing: %v", statErr)
	}
	manifest := readJSONMap(t, filepath.Join(directory, "manifest.json"))
	errorsMap, ok := manifest["artifact_errors"].(map[string]any)
	if !ok || errorsMap["windows.json"] == nil || errorsMap["outputs.json"] == nil {
		t.Fatalf("manifest artifact errors = %v", manifest["artifact_errors"])
	}
}

type testOutputLister struct {
	infos []output.Info
}

func (l *testOutputLister) List(context.Context) ([]output.Info, error) {
	return l.infos, nil
}

func (l *testOutputLister) Close() error { return nil }

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}
