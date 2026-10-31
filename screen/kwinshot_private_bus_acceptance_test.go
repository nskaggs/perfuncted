package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticKWinPipeServer struct {
	mu       sync.Mutex
	captures []string
	fail     bool
	raw      []byte
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
	path := filepath.Join(t.TempDir(), "bus")
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
