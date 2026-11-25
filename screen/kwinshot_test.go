package screen

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"image"
	"image/color"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Only the all-zero rectangle is a full-screen sentinel. Invalid nonzero
// empty rectangles must not reach either KWin transport method.
func TestKWinRejectsNonzeroEmptyCaptureRegionsWithoutDispatch(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			tr := &fakeKWinTransport{active: image.NewRGBA(image.Rect(0, 0, 2, 2)), area: image.NewRGBA(image.Rect(0, 0, 2, 2))}
			b := &KWinShotBackend{transport: tr}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) hash=%08x err=%v; want invalid region", rect, hash, err)
			}
			if tr.activeCalls != 0 || tr.areaCalls != 0 {
				t.Fatalf("invalid region dispatched active=%d area=%d", tr.activeCalls, tr.areaCalls)
			}
		})
	}
}

type syntheticKWinPipeServer struct {
	mu       sync.Mutex
	captures []string
	fail     bool
}

func (p *syntheticKWinPipeServer) CaptureArea(x, y int32, w, h uint32, _ map[string]dbus.Variant, fd dbus.UnixFD) (map[string]dbus.Variant, *dbus.Error) {
	p.mu.Lock()
	p.captures = append(p.captures, fmt.Sprintf("area:%d:%d:%d:%d", x, y, w, h))
	fail := p.fail
	p.mu.Unlock()
	if fail {
		return nil, dbus.MakeFailedError(errors.New("synthetic KWin screenshot permission denied"))
	}
	return p.capture(fd, int(w), int(h))
}
func (p *syntheticKWinPipeServer) CaptureActiveScreen(_ map[string]dbus.Variant, fd dbus.UnixFD) (map[string]dbus.Variant, *dbus.Error) {
	p.mu.Lock()
	p.captures = append(p.captures, "active")
	fail := p.fail
	p.mu.Unlock()
	if fail {
		return nil, dbus.MakeFailedError(errors.New("synthetic KWin screenshot permission denied"))
	}
	return p.capture(fd, 2, 2)
}
func (p *syntheticKWinPipeServer) capture(fd dbus.UnixFD, w, h int) (map[string]dbus.Variant, *dbus.Error) {
	file := os.NewFile(uintptr(fd), "synthetic-kwin-pipe")
	if file == nil {
		return nil, dbus.MakeFailedError(errors.New("invalid KWin pipe"))
	}
	defer file.Close()
	pixels := make([]byte, w*h*4)
	for i := 0; i < len(pixels); i += 4 {
		pixels[i] = 33
		pixels[i+1] = 22
		pixels[i+2] = 11
		pixels[i+3] = 255
	}
	if _, err := file.Write(pixels); err != nil {
		return nil, dbus.MakeFailedError(err)
	}
	return map[string]dbus.Variant{"width": dbus.MakeVariant(uint32(w)), "height": dbus.MakeVariant(uint32(h)), "stride": dbus.MakeVariant(uint32(w * 4)), "format": dbus.MakeVariant(uint32(4))}, nil
}
func startSyntheticKWinBus(t *testing.T, export bool) (string, *syntheticKWinPipeServer) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skipf("dbus-daemon not installed: %v", err)
	}
	path := shortPrivateSocketPath(t, "bus")
	address := "unix:path=" + path
	proc := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	var err error
	for i := 0; i < 100; i++ {
		service, err = dbusutil.ConnectContext(context.Background(), address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("private KWin bus unavailable: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	provider := &syntheticKWinPipeServer{}
	if export {
		reply, err := service.RequestName(kwinShotDest, dbus.NameFlagDoNotQueue)
		if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("synthetic KWin service name: %v %v", reply, err)
		}
		if err := service.Export(provider, dbus.ObjectPath(kwinShotPath), kwinShotIface); err != nil {
			t.Fatal(err)
		}
	}
	return address, provider
}
func TestKWinShotPrivateBusCaptureUsesRealFDProtocolAndCanonicalHashes(t *testing.T) {
	addr, provider := startSyntheticKWinBus(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	b, err := NewKWinShotBackendForBusContext(ctx, addr)
	if err != nil {
		t.Fatalf("synthetic authorized KWin refused: %v", err)
	}
	defer b.Close()
	if !b.CanonicalHashing() {
		t.Fatal("KWin hashing contract missing")
	}
	img, err := b.Grab(ctx, image.Rect(-10, 20, -8, 22))
	if err != nil || img == nil || img.Bounds().Size() != image.Pt(2, 2) {
		t.Fatalf("CaptureArea failed: %+v %v", img, err)
	}
	pixel := img.At(0, 0)
	r, g, bl, a := pixel.RGBA()
	if r>>8 != 11 || g>>8 != 22 || bl>>8 != 33 || a>>8 != 255 {
		t.Fatalf("BGRA pixel was decoded incorrectly: %v", pixel)
	}
	expected := find.PixelHash(img, nil)
	hash, err := b.GrabRegionHash(ctx, image.Rect(-10, 20, -8, 22))
	if err != nil || hash != expected {
		t.Fatalf("region hash %x expected %x err=%v", hash, expected, err)
	}
	full, err := b.GrabFullHash(ctx)
	if err != nil || full != expected {
		t.Fatalf("full hash=%x want=%x err=%v", full, expected, err)
	}
	w, h, err := b.Resolution()
	if err != nil || w != 2 || h != 2 {
		t.Fatalf("resolution=%dx%d err=%v", w, h, err)
	}
	provider.mu.Lock()
	calls := append([]string(nil), provider.captures...)
	provider.mu.Unlock()
	if len(calls) != 5 || calls[0] != "area:0:0:1:1" || calls[1] != "area:-10:20:2:2" {
		t.Fatalf("incorrect DBus method/capture args: %+v", calls)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
}
func TestKWinShotPrivateBusDenialNeverReturnsPixels(t *testing.T) {
	addr, provider := startSyntheticKWinBus(t, true)
	provider.mu.Lock()
	provider.fail = true
	provider.mu.Unlock()
	b, err := NewKWinShotBackendForBusContext(context.Background(), addr)
	if b != nil || err == nil {
		t.Fatalf("KWin authorization denied but capture opened: backend=%v err=%v", b, err)
	}
}
func TestKWinShotPrivateBusMissingServiceAndCancellationFailClosed(t *testing.T) {
	addr, _ := startSyntheticKWinBus(t, false)
	if b, err := NewKWinShotBackendForBusContext(context.Background(), addr); b != nil || err == nil {
		t.Fatalf("missing KWin service was accepted: %v %v", b, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := NewKWinShotBackendForBusContext(canceled, addr); b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled KWin startup: %+v %v", b, err)
	}
	if b, err := NewKWinShotBackendForBusContext(context.Background(), ""); b != nil || err == nil {
		t.Fatal("missing D-Bus address accepted")
	}
}
func TestKWinShotNilBackendAndHashFailuresAreExplicit(t *testing.T) {
	var b *KWinShotBackend
	if _, err := b.Grab(context.Background(), image.Rectangle{}); err == nil {
		t.Fatal("nil backend accepted")
	}
	if _, err := b.GrabRegionHash(context.Background(), image.Rect(1, 1, 2, 2)); err == nil {
		t.Fatal("nil region hash backend accepted")
	}
	if _, err := b.GrabFullHash(context.Background()); err == nil {
		t.Fatal("nil full hash backend accepted")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
}

type fakeKWinTransport struct {
	active      image.Image
	area        image.Image
	activeErr   error
	areaErr     error
	activeCalls int
	areaCalls   int
	areaRects   []image.Rectangle
}

func (f *fakeKWinTransport) CaptureActiveScreen(context.Context) (image.Image, error) {
	f.activeCalls++
	if f.activeErr != nil {
		return nil, f.activeErr
	}
	return f.active, nil
}

func (f *fakeKWinTransport) CaptureArea(_ context.Context, rect image.Rectangle) (image.Image, error) {
	f.areaCalls++
	f.areaRects = append(f.areaRects, rect)
	if f.areaErr != nil {
		return nil, f.areaErr
	}
	return f.area, nil
}

func solidRGBAForKWin(c color.RGBA, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestKWinShotBackend_GrabUsesActiveScreenForEmptyRect(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 10, G: 20, B: 30, A: 255}, 3, 2)
	area := solidRGBAForKWin(color.RGBA{R: 90, G: 80, B: 70, A: 255}, 1, 1)
	transport := &fakeKWinTransport{active: active, area: area}
	b := &KWinShotBackend{transport: transport}

	img, err := b.Grab(context.Background(), image.Rectangle{})
	if err != nil {
		t.Fatalf("Grab(empty): %v", err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
	if img.Bounds() != active.Bounds() {
		t.Fatalf("Grab(empty) bounds = %v, want %v", img.Bounds(), active.Bounds())
	}
	if got, want := find.PixelHash(img, nil), find.PixelHash(active, nil); got != want {
		t.Fatalf("Grab(empty) hash = %08x, want %08x", got, want)
	}
}

func TestKWinShotBackend_GrabUsesAreaForNonEmptyRect(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 10, G: 20, B: 30, A: 255}, 3, 2)
	area := solidRGBAForKWin(color.RGBA{R: 90, G: 80, B: 70, A: 255}, 2, 2)
	transport := &fakeKWinTransport{active: active, area: area}
	b := &KWinShotBackend{transport: transport}
	rect := image.Rect(4, 5, 6, 7)

	img, err := b.Grab(context.Background(), rect)
	if err != nil {
		t.Fatalf("Grab(rect): %v", err)
	}
	if transport.activeCalls != 0 || transport.areaCalls != 1 {
		t.Fatalf("calls = active:%d area:%d, want active:0 area:1", transport.activeCalls, transport.areaCalls)
	}
	if len(transport.areaRects) != 1 || transport.areaRects[0] != rect {
		t.Fatalf("area rects = %v, want %v", transport.areaRects, rect)
	}
	if got, want := img.Bounds(), area.Bounds(); got != want {
		t.Fatalf("Grab(rect) bounds = %v, want %v", got, want)
	}
}

func TestValidateKWinRect(t *testing.T) {
	tests := []struct {
		name string
		rect image.Rectangle
		want bool
	}{
		{name: "valid negative origin", rect: image.Rect(-100, -50, 900, 650), want: true},
		{name: "x origin overflows int32", rect: image.Rect(1<<31, 0, 1<<31+1, 1), want: false},
		{name: "y origin underflows int32", rect: image.Rect(0, -1<<31-1, 1, -1<<31), want: false},
		{name: "width overflows uint32", rect: image.Rectangle{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 1 << 32, Y: 1}}, want: false},
		{name: "height overflows uint32", rect: image.Rectangle{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 1, Y: 1 << 32}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateKWinRect(tt.rect)
			if (err == nil) != tt.want {
				t.Fatalf("validateKWinRect(%v) error = %v, want valid=%v", tt.rect, err, tt.want)
			}
		})
	}
}

func TestKWinShotBackend_GrabFullHashUsesActiveScreen(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 1, G: 2, B: 3, A: 255}, 2, 2)
	transport := &fakeKWinTransport{active: active}
	b := &KWinShotBackend{transport: transport}

	got, err := b.GrabFullHash(context.Background())
	if err != nil {
		t.Fatalf("GrabFullHash: %v", err)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
	if want := find.PixelHash(active, nil); got != want {
		t.Fatalf("GrabFullHash = %08x, want %08x", got, want)
	}
}

func TestKWinShotBackend_ResolutionUsesActiveScreen(t *testing.T) {
	active := solidRGBAForKWin(color.RGBA{R: 4, G: 5, B: 6, A: 255}, 5, 4)
	transport := &fakeKWinTransport{active: active}
	b := &KWinShotBackend{transport: transport}

	w, h, err := b.Resolution()
	if err != nil {
		t.Fatalf("Resolution: %v", err)
	}
	if w != 5 || h != 4 {
		t.Fatalf("Resolution = %dx%d, want 5x4", w, h)
	}
	if transport.activeCalls != 1 || transport.areaCalls != 0 {
		t.Fatalf("calls = active:%d area:%d, want active:1 area:0", transport.activeCalls, transport.areaCalls)
	}
}

func TestKWinPipeDrainContextPreservesPolicyDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	drainCtx, drainCancel := kwinPipeDrainContext(ctx)
	defer drainCancel()
	got, ok := drainCtx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("pipe drain deadline = %v, present=%v, want caller policy deadline %v", got, ok, deadline)
	}
}

func TestDrainKWinPipeHonorsCallerCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, drainErr := drainKWinPipe(ctx, reader)
		result <- drainErr
	}()
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("drainKWinPipe error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("drainKWinPipe did not unblock after caller cancellation")
	}
}

func TestDecodeKWinPixels_UsesResultDimensions(t *testing.T) {
	// Raw ARGB32/BGRA bytes for two opaque pixels.
	data := []byte{
		3, 2, 1, 255,
		6, 5, 4, 255,
	}
	results := map[string]dbus.Variant{
		"width":  dbus.MakeVariant(uint32(2)),
		"height": dbus.MakeVariant(uint32(1)),
		"stride": dbus.MakeVariant(uint32(8)),
		"format": dbus.MakeVariant(uint32(4)),
	}

	img, err := decodeKWinPixels(data, image.Rectangle{}, results)
	if err != nil {
		t.Fatalf("decodeKWinPixels: %v", err)
	}
	if got, want := img.Bounds(), image.Rect(0, 0, 2, 1); got != want {
		t.Fatalf("decodeKWinPixels bounds = %v, want %v", got, want)
	}

	expected := image.NewRGBA(image.Rect(0, 0, 2, 1))
	expected.SetRGBA(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	expected.SetRGBA(1, 0, color.RGBA{R: 4, G: 5, B: 6, A: 255})
	if got, want := find.PixelHash(img, nil), find.PixelHash(expected, nil); got != want {
		t.Fatalf("decodeKWinPixels hash = %08x, want %08x", got, want)
	}
}

func TestDecodeKWinPixelsRejectsInvalidGeometry(t *testing.T) {
	tests := []struct {
		name    string
		results map[string]dbus.Variant
	}{
		{
			name: "short stride",
			results: map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(2)),
				"height": dbus.MakeVariant(uint32(1)),
				"stride": dbus.MakeVariant(uint32(4)),
			},
		},
		{
			name: "zero dimensions",
			results: map[string]dbus.Variant{
				"width":  dbus.MakeVariant(uint32(0)),
				"height": dbus.MakeVariant(uint32(1)),
				"stride": dbus.MakeVariant(uint32(4)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeKWinPixels(make([]byte, 8), image.Rectangle{}, tt.results); err == nil {
				t.Fatal("decodeKWinPixels unexpectedly accepted invalid geometry")
			}
		})
	}
}
