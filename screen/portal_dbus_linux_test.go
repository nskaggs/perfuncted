//go:build linux

package screen

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A portal-controlled screenshot file might be a FIFO that never sends PNG
// bytes. Cancellation must interrupt the in-flight decoder, not merely be
// checked after the pipe unexpectedly reaches EOF.
func TestScreenshotPortalCancellationInterruptsBlockedPNGTransport(t *testing.T) {
	b, p := newSyntheticScreenshotPortal(t)
	fifo := filepath.Join(t.TempDir(), "withheld-pixels.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("FIFO unsupported: %v", err)
	}
	p.mu.Lock()
	p.uri = "file://" + fifo
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type captureResult struct {
		image image.Image
		err   error
	}
	result := make(chan captureResult, 1)
	go func() { img, err := b.Grab(ctx, image.Rectangle{}); result <- captureResult{img, err} }()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err == nil {
			opened <- f
		}
	}()
	var writer *os.File
	select {
	case writer = <-opened:
	case <-ctx.Done():
		t.Fatal("capture transport never opened")
	}
	defer writer.Close()
	cancel()
	select {
	case value := <-result:
		if !errors.Is(value.err, context.Canceled) || value.image != nil {
			t.Fatalf("canceling stalled capture returned pixels=%v error=%v", value.image, value.err)
		}
	case <-time.After(150 * time.Millisecond):
		// Unblock a broken decoder before failing so this regression test never
		// leaves an unbounded provider-read goroutine in the suite.
		_ = writer.Close()
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("cancellation failed to interrupt stalled PNG transport")
	}
}

// A portal provider can hand back a pipe-like file which remains readable
// after the caller cancels. The screenshot must not be published after that
// cancel, even when a syntactically valid PNG arrives later.
func TestScreenshotPortalNeverPublishesImageAfterCancellationDuringDecode(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	fifo := filepath.Join(t.TempDir(), "private-capture.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + fifo
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		img image.Image
		err error
	}
	done := make(chan result, 1)
	go func() { img, err := backend.Grab(ctx, image.Rectangle{}); done <- result{img, err} }()
	writer := make(chan *os.File, 1)
	writerErr := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			writerErr <- err
			return
		}
		writer <- f
	}()
	var f *os.File
	select {
	case f = <-writer:
	case err := <-writerErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatalf("portal did not reach screenshot file decode: %v", ctx.Err())
	}
	// Both reader and writer have opened the FIFO. The reader is blocked on
	// PNG bytes, not on a portal reply, and cancellation is now deterministic.
	cancel()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
	// Cancellation may close the FIFO reader before the writer finishes.
	// EPIPE is expected in that case; the capture must still return no pixels.
	if err := png.Encode(f, img); err != nil && !errors.Is(err, unix.EPIPE) {
		_ = f.Close()
		t.Fatalf("unexpected PNG transport failure after cancellation: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-done:
		if !errors.Is(value.err, context.Canceled) || value.img != nil {
			t.Fatalf("canceled screenshot leaked private pixels: img=%v err=%v", value.img, value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("PNG decoder never released canceled screenshot request")
	}
}

// A closed screenshot transport is no longer authorized to perform captures,
// even when its provider still owns the well-known portal service name.
func TestPortalCaptureRejectsClosedClientTransportWithoutPixelsOrGateLeak(t *testing.T) {
	backend, provider := newSyntheticScreenshotPortal(t)
	if err := backend.Close(); err != nil {
		t.Fatalf("close portal transport: %v", err)
	}
	if !provider.conn.Connected() {
		t.Fatal("closing client also disconnected portal provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if err == nil || img != nil {
		t.Fatalf("capture after client transport close: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("closed portal capture leaked admission gate")
	}
	if !provider.conn.Connected() {
		t.Fatal("failed client capture disrupted independent portal provider")
	}
}

// The consent-gated screenshot backend must not start another portal request
// while the first one owns its capture slot. A canceled waiting caller cannot
// poison the gate, and a timed-out owner cannot strand subsequent captures.
func TestSyntheticScreenshotPortalConcurrentGateCancellationAndRecovery(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	backend.SetCaptureTimeout(120 * time.Millisecond)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	firstDone := make(chan error, 1)
	go func() { _, err := backend.Grab(context.Background(), image.Rectangle{}); firstDone <- err }()
	gateDeadline := time.After(time.Second)
	for len(backend.captureGate) != 1 {
		select {
		case <-gateDeadline:
			t.Fatal("first screenshot never acquired capture slot")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	img, err := backend.Grab(ctx, image.Rectangle{})
	if img != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked second screenshot acknowledged: image=%v err=%v", img, err)
	}
	select {
	case err := <-firstDone:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("unanswered first capture not bounded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first capture did not release slot on timeout")
	}
	if len(backend.captureGate) != 0 {
		t.Fatalf("capture slot leaked after timeout")
	}
	filePath := filepath.Join(t.TempDir(), "recovered.png")
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	pngData := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pngData.SetRGBA(1, 1, color.RGBA{R: 200, A: 255})
	if err := png.Encode(f, pngData); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.skip = false
	portal.uri = "file://" + filePath
	portal.mu.Unlock()
	restored, err := backend.Grab(context.Background(), image.Rect(1, 1, 2, 2))
	if err != nil || restored == nil || restored.Bounds().Dx() != 1 || restored.Bounds().Dy() != 1 {
		t.Fatalf("post-cancellation portal did not recover: img=%v err=%v", restored, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("successful request did not release capture slot")
	}
}

// A screenshot portal supplies URI paths, which may encode sensitive filenames
// or directory components. The per-backend diagnostic is long lived and can
// appear in capabilities or user-visible failure reports, unlike the direct
// error returned to the caller. It must never publish that untrusted path.
func TestScreenshotPortalDiagnosticsNeverExposePrivateURIPaths(t *testing.T) {
	const secret = "private-health-record-ACCESS-TOKEN"
	for _, tc := range []struct{ name, uri, reason string }{
		{"malformed URI", "file:///tmp/" + secret + "%", "invalid screenshot URI"},
		{"missing private file", "file:///tmp/" + secret + "-not-present.png", "unable to open screenshot file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			portal.mu.Lock()
			portal.uri = tc.uri
			portal.mu.Unlock()
			img, err := backend.Grab(context.Background(), image.Rectangle{})
			if err == nil || img != nil {
				t.Fatalf("invalid URI accepted: img=%v err=%v", img, err)
			}
			details := strings.Join(backend.Diagnostics(), " ")
			if strings.Contains(details, secret) {
				t.Fatalf("persistent screenshot diagnostics leaked private portal URI: %s", details)
			}
			if !strings.Contains(details, tc.reason) {
				t.Fatalf("diagnostic missed safe failure classification %q: %s", tc.reason, details)
			}
		})
	}
}

func TestScreenshotPortalDiagnosticsBoundUntrustedErrorLength(t *testing.T) {
	backend := &PortalDBusBackend{}
	backend.recordFailure(errors.New(strings.Repeat("malformed-portal-error", 100000)))
	details := strings.Join(backend.Diagnostics(), " ")
	if len(details) > 1024 {
		t.Fatalf("untrusted portal failure allocated %d diagnostic bytes", len(details))
	}
	if !strings.Contains(details, "last failure:") {
		t.Fatalf("missing bounded error diagnostic: %s", details)
	}
}

func TestScreenshotPortalDiagnosticsTruncateOnWholeUTF8Boundary(t *testing.T) {
	backend := &PortalDBusBackend{}
	backend.recordFailure(errors.New(strings.Repeat("x", 255) + "é" + strings.Repeat("sensitive", 100)))
	backend.healthMu.RLock()
	retained := backend.lastFailure
	backend.healthMu.RUnlock()
	if !utf8.ValidString(retained) || len(retained) > 256 {
		t.Fatalf("bounded diagnostic invalid UTF-8 or size: bytes=%d valid=%v", len(retained), utf8.ValidString(retained))
	}
	for _, text := range []string{
		"screen/portal: close root for /private/important.png: inaccessible",
		"screen/portal: close /private/important.png: inaccessible",
		"screen/portal: file:///private/important.png failed",
	} {
		backend.recordFailure(errors.New(text))
		backend.healthMu.RLock()
		got := backend.lastFailure
		backend.healthMu.RUnlock()
		if strings.Contains(got, "/private/") {
			t.Fatalf("retained screenshot path from %q as %q", text, got)
		}
	}
}

// Only the zero rectangle is the documented full-screen sentinel. Other
// empty or inverted rectangles must not silently disclose a full screenshot.
func TestPortalRejectsEmptyNonzeroCaptureRegionsBeforeConsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"inverted", image.Rectangle{Min: image.Pt(5, 5), Max: image.Pt(2, 2)}},
		{"zero width", image.Rect(2, 2, 2, 4)},
		{"offset empty", image.Rect(2, 2, 2, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, portal := newSyntheticScreenshotPortal(t)
			b.SetCaptureTimeout(70 * time.Millisecond)
			portal.mu.Lock()
			portal.skip = true
			portal.mu.Unlock()
			img, err := b.Grab(context.Background(), tc.rect)
			if err == nil || img != nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("invalid region %v returned image=%v error=%v", tc.rect, img, err)
			}
		})
	}
}

// A policy shorter than portal transport startup must never authorize pixels,
// even when a real, connected private D-Bus screenshot provider is available.
func TestPortalImmediateCapturePolicyExpirationNeverPublishesPixels(t *testing.T) {
	backend, _ := newSyntheticScreenshotPortal(t)
	backend.SetCaptureTimeout(time.Nanosecond)

	for i := 0; i < 5; i++ {
		img, err := backend.Grab(context.Background(), image.Rectangle{})
		if img != nil {
			t.Fatalf("request %d published pixels after expiry: %v", i, img.Bounds())
		}
		if !errors.Is(err, context.DeadlineExceeded) && (err == nil || !strings.Contains(err.Error(), "timed out waiting for screenshot")) {
			t.Fatalf("request %d error = %v, want policy deadline", i, err)
		}
		if len(backend.captureGate) != 0 {
			t.Fatalf("request %d leaked screenshot admission gate", i)
		}
	}
}

// The portal delivers a full-screen PNG. A requested region completely
// outside that image is not a successful capture of transparent pixels.
func TestSyntheticScreenshotPortalRejectsWhollyOutsideCaptureRegion(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "screen.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + path
	portal.mu.Unlock()
	for _, rect := range []image.Rectangle{image.Rect(8, 8, 10, 10), image.Rect(-10, -10, -8, -8)} {
		img, err := backend.Grab(context.Background(), rect)
		if err == nil || img != nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("out-of-bounds screenshot %v returned image=%v error=%v", rect, img, err)
		}
	}
}

// A revoked capture must not progress to backend initialization or expose pixels,
// even when the caller supplies a malformed region or a disconnected backend.
func TestPortalGrabRevokedContextPrecedesBackendAndRegionChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name    string
		backend *PortalDBusBackend
		rect    image.Rectangle
	}{
		{name: "nil backend and full capture"},
		{name: "disconnected backend and invalid rectangle", backend: &PortalDBusBackend{}, rect: image.Rect(4, 4, 4, 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := tc.backend.Grab(ctx, tc.rect)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Grab() error = %v, want context.Canceled", err)
			}
			if img != nil {
				t.Fatalf("Grab() returned pixels for a revoked capture: %v", img.Bounds())
			}
		})
	}
}

// This private D-Bus service can send structurally malformed and omitted
// response fields, unlike the normal synthetic service which always adds a
// file URI. The test runs the real portal admission and signal routing path.
type portalResponseMetadataFixture struct {
	conn        *dbus.Conn
	code        any
	results     any
	blankHandle bool
}

func (f *portalResponseMetadataFixture) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = f.conn.Emit(path, portalReqIf+".Response", f.code, f.results)
	}()
	if f.blankHandle {
		return "", nil
	}
	return path, nil
}

func TestScreenshotPortalResponseMetadataMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		code        any
		results     func(string) any
		blankHandle bool
		want        string
		successful  bool
	}{
		{"valid portal result", uint32(0), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, false, "", true},
		// A blank object path is invalid on the D-Bus wire and must be refused.
		{"invalid empty request handle", uint32(0), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, true, "invalid message", false},
		{"wrong result type", uint32(0), func(string) any { return "not a variant map" }, false, "no URI", false},
		{"missing URI", uint32(0), func(string) any { return map[string]dbus.Variant{} }, false, "no URI", false},
		{"wrong URI variant type", uint32(0), func(string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant(true)} }, false, "unsupported URI scheme", false},
		{"not authorized", uint32(2), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, false, "denied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, base := newSyntheticScreenshotPortal(t)
			path := filepath.Join(t.TempDir(), "portal-result.png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			fixture := &portalResponseMetadataFixture{conn: base.conn, code: tc.code, results: tc.results(path), blankHandle: tc.blankHandle}
			if err := base.conn.Export(fixture, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Grab(context.Background(), image.Rectangle{})
			if tc.successful {
				if err != nil || got == nil || got.Bounds() != image.Rect(0, 0, 4, 3) {
					t.Fatalf("valid fallback response image=%v err=%v", got, err)
				}
			} else {
				if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("malformed response image=%v err=%v want=%q", got, err, tc.want)
				}
			}
			if len(backend.captureGate) != 0 {
				t.Fatal("portal response leaked screenshot gate")
			}
		})
	}
}

func TestScreenshotFileURIDecodingAndPathAuthorityMatrix(t *testing.T) {
	for _, tc := range []struct {
		uri, want string
		ok        bool
	}{
		{"file:///tmp/photo%20name.png", "/tmp/photo name.png", true},
		{"file://localhost/tmp/%C3%A9.png", "/tmp/é.png", true},
		{"file:///tmp/%%", "", false},
		{"file:///tmp/%", "", false},
		{"file:///tmp/%2", "", false},
		{"file:///tmp/%ff", "", false},
		{"file:///tmp/%00file.png", "", false},
		{"file://remotehost/tmp/photo.png", "", false},
		{"https:///tmp/photo.png", "", false},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			got, err := fileURIPath(tc.uri)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("URI=%q got=%q err=%v", tc.uri, got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("invalid URI=%q accepted as %q err=%v", tc.uri, got, err)
			}
		})
	}
}

func TestScreenshotPortalURIRejectsDecodedNULAndInvalidUTF8(t *testing.T) {
	for _, uri := range []string{
		"file:///tmp/capture%00secret.png",
		"file:///tmp/capture%FF.png",
		"file:///tmp/capture%C0%AF.png",
		"file:///tmp/capture%E2%82.png",
		"file:///tmp/" + string([]byte{0xfe}) + ".png",
	} {
		t.Run(uri, func(t *testing.T) {
			path, err := fileURIPath(uri)
			if err == nil || path != "" {
				t.Fatalf("untrusted portal screenshot URI accepted: path=%q err=%v", path, err)
			}
		})
	}
	for _, tc := range []struct{ uri, want string }{
		{"file:///tmp/capture%20safe.png", "/tmp/capture safe.png"},
		{"file://localhost/tmp/capture%2Bplus.png", "/tmp/capture+plus.png"},
	} {
		path, err := fileURIPath(tc.uri)
		if err != nil || !strings.EqualFold(path, tc.want) {
			t.Fatalf("valid portal URI incorrectly rejected: path=%q err=%v", path, err)
		}
	}
}
