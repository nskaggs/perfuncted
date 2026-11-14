package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recoverableCaptureBackend struct {
	fail  error
	calls int
}

func (b *recoverableCaptureBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	b.calls++
	if b.fail != nil {
		return nil, b.fail
	}
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}
func (*recoverableCaptureBackend) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*recoverableCaptureBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*recoverableCaptureBackend) Close() error { return nil }

// Backend failures and staging-file failures must fail closed without
// modifying an existing screenshot. The same session must remain usable once
// the transient backend failure has cleared.
func TestScreenBundleCaptureFailureDoesNotPublishOrPoisonRecovery(t *testing.T) {
	cause := errors.New("transient screen transport failure")
	backend := &recoverableCaptureBackend{fail: cause}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	defer session.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "screenshot.png")
	const old = "preserve the last authorized screenshot"
	if err := os.WriteFile(target, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	img, err := session.Screen.Grab(context.Background(), image.Rectangle{})
	if img != nil || !errors.Is(err, cause) || !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("backend failure leaked image: image=%v err=%v", img, err)
	}
	err = session.Screen.CaptureRegion(context.Background(), image.Rectangle{}, target)
	if !errors.Is(err, cause) || !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("failed capture did not preserve backend cause: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != old {
		t.Fatalf("failed capture replaced previous screenshot: %q %v", data, readErr)
	}
	backend.fail = nil
	inaccessible := filepath.Join(dir, "missing-parent", "capture.png")
	if err := session.Screen.CaptureRegion(context.Background(), image.Rectangle{}, inaccessible); err == nil || !errors.Is(err, ErrOperationFailed) || !strings.Contains(err.Error(), "create") {
		t.Fatalf("missing staging directory did not fail closed: %v", err)
	}
	if _, err := os.Stat(inaccessible); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed staging unexpectedly published screenshot: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "screenshot.png" {
		t.Fatalf("failure left staging artifacts: %v %v", entries, err)
	}
	if err := session.Screen.CaptureRegion(context.Background(), image.Rectangle{}, target); err != nil {
		t.Fatalf("capture did not recover: %v", err)
	}
	data, err = os.ReadFile(target)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("recovery did not publish valid PNG: %v", err)
	}
	if backend.calls != 4 {
		t.Fatalf("unexpected captures: %d, want four", backend.calls)
	}
}
