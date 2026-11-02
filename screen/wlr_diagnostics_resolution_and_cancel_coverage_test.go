package screen

import (
	"context"
	"errors"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlrBackendMissingSessionClosedAndCanceledOperations(t *testing.T) {
	var absent *WlrScreencopyBackend
	if reports := absent.Diagnostics(); len(reports) != 1 || reports[0] != "backend unavailable" {
		t.Fatalf("nil diagnostics=%v", reports)
	}
	if w, h, err := absent.ResolutionWithContext(context.Background()); w != 0 || h != 0 || err == nil {
		t.Fatalf("nil resolution=%d,%d %v", w, h, err)
	}
	if err := absent.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil {
		t.Fatal("nil backend accepted context operation")
	}
	if err := absent.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWlrFailedConnectionRecordsHealthAndRejectsClosedOperations(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector(filepath.Join(t.TempDir(), "does-not-exist"), nil, 0)
	if b.ttl <= 0 || b.connect == nil {
		t.Fatalf("defaulted backend invalid: ttl=%s", b.ttl)
	}
	if reports := b.Diagnostics(); len(reports) < 3 || !strings.Contains(strings.Join(reports, ";"), "no recorded failures") {
		t.Fatalf("fresh health=%v", reports)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.captureFrame(cancelled, func([]byte, bufInfo) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture accepted: %v", err)
	}
	if w, h, err := b.ResolutionWithContext(cancelled); w != 0 || h != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolution=%d,%d %v", w, h, err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Fatalf("unreachable compositor accepted: %v", err)
	}
	if reports := b.Diagnostics(); !strings.Contains(strings.Join(reports, ";"), "last failure") {
		t.Fatalf("failed connection not recorded: %v", reports)
	}
	if w, h, err := b.Resolution(); w != 0 || h != 0 || err == nil {
		t.Fatalf("unreachable resolution=%d,%d %v", w, h, err)
	}
	assertWlrCloseAndNoReuse(t, b)
}

func TestWlrPublicSocketConstructorRejectsInvalidTargetsBeforeTransport(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := NewWlrScreencopyBackendForSocketContext(cancelled, "invalid"); b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor=%v %v", b, err)
	}
	if b, err := NewWlrScreencopyBackendForSocketContext(context.Background(), ""); b != nil || err == nil {
		t.Fatalf("empty socket accepted: %v %v", b, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.sock")
	if b, err := NewWlrScreencopyBackendForSocketContext(context.Background(), missing); b != nil || err == nil {
		t.Fatalf("unreachable socket accepted: %v %v", b, err)
	}
}

func TestWlrCachedResolutionPreservesMeasuredDimensionsWithoutCapture(t *testing.T) {
	b := newWlrScreencopyBackendWithContextConnector("not-connected", nil, time.Minute)
	b.pW, b.pH = 1920, 1080
	w, h, err := b.ResolutionWithContext(context.Background())
	if err != nil || w != 1920 || h != 1080 {
		t.Fatalf("cached resolution %dx%d %v", w, h, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWlrRawProxyDispatchContextAccessorAndFailedFrameBoundary(t *testing.T) {
	proxy := &wlRawProxy{}
	var opcode uint32
	proxy.dispatchFn = func(next uint32, _ int, _ []byte) { opcode = next }
	proxy.SetID(42)
	if proxy.ID() != 42 {
		t.Fatalf("proxy identity=%d", proxy.ID())
	}
	proxy.Dispatch(3, -1, nil)
	if opcode != 3 {
		t.Fatalf("failed frame not dispatched: %d", opcode)
	}
	var bi bufInfo
	ready, failed, done := false, false, false
	applyWlrFrameEvent(&bi, &ready, &failed, &done, 6, nil)
	if !done || failed || ready {
		t.Fatalf("buffer completion state ready=%v failed=%v done=%v", ready, failed, done)
	}
	_ = proxy.Ctx()
	_ = image.Rectangle{}
}

func assertWlrCloseAndNoReuse(t *testing.T, b *WlrScreencopyBackend) {
	t.Helper()

	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if reports := b.Diagnostics(); !strings.Contains(strings.Join(reports, ";"), "connection health: closed") {
		t.Fatalf("closed diagnostics=%v", reports)
	}
	if w, h, err := b.ResolutionWithContext(context.Background()); w != 0 || h != 0 || err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed resolution=%d,%d %v", w, h, err)
	}
	if err := b.withWlrContextContext(context.Background(), func(*wl.Context, context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed backend accepted operation: %v", err)
	}
}
