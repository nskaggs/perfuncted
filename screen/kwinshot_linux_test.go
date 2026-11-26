//go:build linux

package screen

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/find"
	"golang.org/x/sys/unix"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Invalid geometry is rejected before transport or pipe allocation. This is
// a direct test of KWin's CaptureArea boundary, not the public Grab wrapper.
func TestKWinCaptureAreaRejectsMalformedGeometryBeforeDBus(t *testing.T) {
	transport := &kwinDBusTransport{}
	cases := []struct {
		name string
		rect image.Rectangle
		want string
	}{
		{"empty rectangle", image.Rect(0, 0, 0, 0), "empty rectangle"},
		{"negative width", image.Rectangle{Min: image.Pt(5, 0), Max: image.Pt(2, 1)}, "empty rectangle"},
		{"negative height", image.Rectangle{Min: image.Pt(0, 5), Max: image.Pt(1, 2)}, "empty rectangle"},
		{"x origin underflow", image.Rect(-1<<31-1, 0, -1<<31+1, 1), "x origin"},
		{"x origin overflow", image.Rect(1<<31, 0, 1<<31+1, 1), "x origin"},
		{"y origin underflow", image.Rect(0, -1<<31-1, 1, -1<<31+1), "y origin"},
		{"y origin overflow", image.Rect(0, 1<<31, 1, 1<<31+1), "y origin"},
		{"width overflows uint32", image.Rect(-10, 0, 1<<33, 1), "width"},
		{"height overflows uint32", image.Rect(0, -10, 1, 1<<33), "height"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, err := transport.CaptureArea(context.Background(), tc.rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid capture geometry %+v gave image=%v err=%v", tc.rect, img, err)
			}
		})
	}
}

type kwinReplyFixture struct {
	dbus.BusObject
	body []any
	hold *os.File
}

func (f *kwinReplyFixture) CallWithContext(_ context.Context, _ string, _ dbus.Flags, args ...any) *dbus.Call {
	if f.body == nil {
		fd, ok := args[len(args)-1].(dbus.UnixFD)
		if !ok {
			return &dbus.Call{Err: errors.New("missing capture descriptor")}
		}
		duplicate, err := unix.Dup(int(fd))
		if err != nil {
			return &dbus.Call{Err: err}
		}
		f.hold = os.NewFile(uintptr(duplicate), "synthetic-kwin-writer")
		return &dbus.Call{Body: []any{map[string]dbus.Variant{}}}
	}
	return &dbus.Call{Body: f.body}
}

func TestKWinCaptureRejectsMalformedReplyAndInterruptedPipe(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []any
		want string
	}{
		{name: "malformed metadata", body: []any{"not metadata"}, want: "store results"},
		{name: "unclosed writer", want: "read pipe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &kwinReplyFixture{body: tc.body}
			transport := &kwinDBusTransport{kwin: fixture}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			img, err := transport.CaptureActiveScreen(ctx)
			if fixture.hold != nil {
				defer fixture.hold.Close()
			}
			if img != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unusable KWin capture returned image=%v err=%v, want %q", img, err, tc.want)
			}
			if tc.body == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("stalled capture did not report a timeout: %v", err)
			}
		})
	}
}

// A compositor capture whose read descriptor was already closed cannot be
// reported as a successful, empty screenshot. Propagate the pipe deadline
// setup failure while retaining the underlying closed-descriptor identity.
func TestDrainKWinPipeRejectsClosedReadDescriptor(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	pixels, err := drainKWinPipe(context.Background(), reader)
	if len(pixels) != 0 || err == nil || !strings.Contains(err.Error(), "pipe deadline") || !strings.Contains(err.Error(), "closed file") {
		t.Fatalf("closed KWin capture pipe: pixels=%d err=%v", len(pixels), err)
	}
}

// Closing the KWin wrapper must not take ownership of an injected transport
// that has no private D-Bus connection. Its capture operations remain usable.
func TestKWinBackendCloseDoesNotRetireInjectedTransport(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	transport := &fakeKWinTransport{active: source, area: source}
	backend := &KWinShotBackend{transport: transport}
	if err := backend.Close(); err != nil {
		t.Fatalf("close injected transport wrapper: %v", err)
	}
	got, err := backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || got == nil || got.Bounds() != source.Bounds() {
		t.Fatalf("closing wrapper retired borrowed transport: image=%v err=%v", got, err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeated close injected transport wrapper: %v", err)
	}
}

func TestKWinCaptureFailsClosedWhenPipeDescriptorsExhausted(t *testing.T) {
	const childEnv = "PF_TEST_KWIN_DESCRIPTOR_LIMIT"
	if os.Getenv(childEnv) == "1" {
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		if limited.Cur > 64 {
			limited.Cur = 64
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		var files []*os.File
		for {
			f, err := os.Open("/dev/null")
			if errors.Is(err, syscall.EMFILE) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
		img, captureErr := (&kwinDBusTransport{}).CaptureActiveScreen(context.Background())
		for _, f := range files {
			if err := f.Close(); err != nil {
				t.Errorf("release test descriptor: %v", err)
			}
		}
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		if img != nil || !errors.Is(captureErr, syscall.EMFILE) || !strings.Contains(captureErr.Error(), "pipe") {
			t.Fatalf("descriptor exhaustion returned image=%v err=%v", img, captureErr)
		}
		return
	}

	args := []string{"-test.run=^TestKWinCaptureFailsClosedWhenPipeDescriptorsExhausted$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor test failed: %v\n%s", err, output)
	}
}

// A tightly packed QImage RGBA8888 buffer must not take the BGRA conversion
// path. The decoder owns the returned frame and preserves the pixel channels,
// including a nonopaque alpha value, without leaking transport metadata.
func TestKWinPackedRGBA8888PreservesTransparentPixels(t *testing.T) {
	pixels := []byte{8, 27, 46, 255, 91, 83, 72, 128, 1, 2, 3, 64, 255, 201, 34, 0}
	metadata := map[string]dbus.Variant{
		"width": dbus.MakeVariant(uint32(2)), "height": dbus.MakeVariant(uint32(2)),
		"stride": dbus.MakeVariant(uint32(8)), "format": dbus.MakeVariant(uint32(17)),
	}
	img, err := decodeKWinPixels(pixels, image.Rectangle{}, metadata)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("packed RGBA decoded into unexpected type %T", img)
	}
	want := []color.NRGBA{{8, 27, 46, 255}, {91, 83, 72, 128}, {1, 2, 3, 64}, {255, 201, 34, 0}}
	for i, expected := range want {
		x, y := i%2, i/2
		got := decoded.NRGBAAt(x, y)
		if got != expected {
			t.Fatalf("packed RGBA pixel %d changed: got %+v want %+v", i, got, expected)
		}
	}
	// Changing the transport buffer after decoding cannot change the image.
	for i := range pixels {
		pixels[i] = 0
	}
	if got := decoded.NRGBAAt(0, 0); got != want[0] {
		t.Fatalf("decoded image retained transport memory: %+v", got)
	}
}

func TestKWinEmptyRegionHashUsesAuthorizedFullScreenCapture(t *testing.T) {
	source := solidRGBAForKWin(color.RGBA{R: 20, G: 30, B: 40, A: 255}, 3, 2)
	transport := &fakeKWinTransport{active: source}
	backend := &KWinShotBackend{transport: transport}
	hash, err := backend.GrabRegionHash(context.Background(), image.Rectangle{})
	if err != nil || hash != find.PixelHash(source, nil) {
		t.Fatalf("empty region hash=%08x err=%v", hash, err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("empty region used wrong screenshot authority: %+v", transport)
	}
}

func TestKWinTransportRejectsInvalidCaptureBeforeCallingDBus(t *testing.T) {
	transport := &kwinDBusTransport{}
	if img, err := transport.CaptureArea(context.Background(), image.Rectangle{}); img != nil || err == nil || !strings.Contains(err.Error(), "empty rectangle") {
		t.Fatalf("empty capture area reached transport: %v %v", img, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if img, err := transport.CaptureActiveScreen(ctx); img != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture reached transport: %v %v", img, err)
	}
}

func TestKWinRawPixelDecodeSupportsRegionWithoutMetadata(t *testing.T) {
	rect := image.Rect(10, 20, 11, 21)
	img, err := decodeKWinPixels([]byte{33, 22, 11, 255}, rect, nil)
	if err != nil {
		t.Fatalf("raw BGRA without metadata: %v", err)
	}
	decoded, ok := img.(*image.NRGBA)
	if !ok {
		t.Fatalf("raw BGRA decoded into unexpected type %T", img)
	}
	if got := decoded.NRGBAAt(0, 0); got != (color.NRGBA{11, 22, 33, 255}) {
		t.Fatalf("raw capture without metadata changed channels: %+v", got)
	}
}

func TestKWinCaptureCanceledBeforeDispatchDoesNotCallCompositor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &fakeKWinTransport{active: image.NewRGBA(image.Rect(0, 0, 2, 2)), area: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	b := &KWinShotBackend{transport: transport}
	for _, rect := range []image.Rectangle{{}, image.Rect(0, 0, 1, 1)} {
		img, err := b.Grab(ctx, rect)
		if img != nil || !errors.Is(err, context.Canceled) {
			t.Errorf("Grab(%v) = %v, %v; want cancellation", rect, img, err)
		}
	}
	if transport.activeCalls != 0 || transport.areaCalls != 0 {
		t.Errorf("canceled captures dispatched to compositor: active=%d, area=%d", transport.activeCalls, transport.areaCalls)
	}
}

type lateCanceledKWinTransport struct {
	cancel                 context.CancelFunc
	activeCalls, areaCalls int
}

func (t *lateCanceledKWinTransport) CaptureActiveScreen(context.Context) (image.Image, error) {
	t.activeCalls++
	t.cancel()
	return image.NewRGBA(image.Rect(0, 0, 4, 3)), nil
}
func (t *lateCanceledKWinTransport) CaptureArea(context.Context, image.Rectangle) (image.Image, error) {
	t.areaCalls++
	t.cancel()
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}

// Successful compositor replies cannot override an operation canceled while
// capture was in flight, and the returned pixels must not escape to callers.
func TestKWinLateCancellationDiscardsCapturedPixels(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rect         image.Rectangle
		active, area int
	}{
		{"full screen", image.Rectangle{}, 1, 0},
		{"region", image.Rect(0, 0, 2, 2), 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := &lateCanceledKWinTransport{cancel: cancel}
			backend := &KWinShotBackend{transport: transport}
			img, err := backend.Grab(ctx, tc.rect)
			if img != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("late canceled capture: img=%v err=%v", img, err)
			}
			if transport.activeCalls != tc.active || transport.areaCalls != tc.area {
				t.Fatalf("dispatch count active=%d area=%d", transport.activeCalls, transport.areaCalls)
			}
		})
	}
}

// A compositor transport that produces no frame must not turn into a valid
// zero hash or a panic in the public resolution API.
func TestKWinShotRejectsMissingFrameAcrossOperations(t *testing.T) {
	b := &KWinShotBackend{transport: &fakeKWinTransport{}}
	ctx := context.Background()
	checks := []struct {
		name   string
		invoke func() error
	}{
		{"active capture", func() error { _, err := b.Grab(ctx, image.Rectangle{}); return err }},
		{"area capture", func() error { _, err := b.Grab(ctx, image.Rect(0, 0, 3, 2)); return err }},
		{"full hash", func() error { _, err := b.GrabFullHash(ctx); return err }},
		{"region hash", func() error { _, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 3, 2)); return err }},
		{"resolution", func() error { _, _, err := b.Resolution(); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.invoke(); err == nil {
				t.Fatal("nil capture was accepted as a successful operation")
			}
		})
	}
}

// KWin sends untrusted byte buffers and stride/format metadata over D-Bus.
// Verify padding bytes never leak into visible pixels and missing rows fail closed.
func TestKWinRawCaptureDecodesRowsWithPaddedStrides(t *testing.T) {
	// Distinct colored pixels, with poison padding after each row.
	for _, tc := range []struct {
		name   string
		format uint32
		data   []byte
	}{
		{"rgba", 17, []byte{10, 20, 30, 255, 90, 91, 92, 93, 40, 50, 60, 255, 94, 95, 96, 97}},
		{"bgra", 4, []byte{30, 20, 10, 255, 90, 91, 92, 93, 60, 50, 40, 255, 94, 95, 96, 97}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(1)),
				"height": dbus.MakeVariant(uint32(2)),
				"stride": dbus.MakeVariant(uint32(8)),
				"format": dbus.MakeVariant(tc.format),
			}
			img, err := decodeKWinPixels(tc.data, image.Rectangle{}, meta)
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds() != image.Rect(0, 0, 1, 2) {
				t.Fatalf("bounds %v", img.Bounds())
			}
			for y, want := range []color.NRGBA{{10, 20, 30, 255}, {40, 50, 60, 255}} {
				if got := color.NRGBAModel.Convert(img.At(0, y)).(color.NRGBA); got != want { //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
					t.Fatalf("row %d pixel = %+v, want %+v", y, got, want)
				}
			}
			for _, truncated := range []int{0, 4, 8, 12, 15} {
				if _, err := decodeKWinPixels(tc.data[:truncated], image.Rectangle{}, meta); err == nil || !strings.Contains(err.Error(), "short pixel buffer") {
					t.Fatalf("truncated to %d bytes accepted: %v", truncated, err)
				}
			}
		})
	}
}

func TestKWinPNGPixelCaptureIgnoresUntrustedRawFormatMetadata(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{1, 2, 3, 255})
	source.SetNRGBA(1, 0, color.NRGBA{4, 5, 6, 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	// PNG is a supported alternate KWin transport format. Its self-describing
	// dimensions and pixels take precedence over raw QImage format metadata.
	meta := map[string]dbus.Variant{"width": dbus.MakeVariant(uint32(1)), "height": dbus.MakeVariant(uint32(1)), "format": dbus.MakeVariant(uint32(4))}
	got, err := decodeKWinPixels(encoded.Bytes(), image.Rectangle{}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", got.Bounds(), source.Bounds())
	}
	for x := 0; x < 2; x++ {
		if color.NRGBAModel.Convert(got.At(x, 0)).(color.NRGBA) != color.NRGBAModel.Convert(source.At(x, 0)).(color.NRGBA) { //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
			t.Fatalf("pixel %d differs: %v != %v", x, got.At(x, 0), source.At(x, 0))
		}
	}
}
