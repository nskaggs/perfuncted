//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xtest"
)

// This lane owns a real native X11 framebuffer; the independent X client
// establishes expected pixels rather than deriving expectations from pf.
func TestReleaseVisualAcceptanceX11(t *testing.T) {
	s := mustSuite(t)
	if s.mode != displayHeadlessX11 && s.mode != displayNestedX11 {
		t.Fatal("release visual acceptance requires isolated headless or nested X11")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := xgb.NewConnDisplay(s.rt.Get("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	root := xproto.Setup(conn).DefaultScreen(conn)
	win, err := xproto.NewWindowId(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateWindowChecked(conn, root.RootDepth, win, root.Root, 40, 40, 420, 200, 0,
		xproto.WindowClassInputOutput, root.RootVisual, xproto.CwBackPixel|xproto.CwOverrideRedirect,
		[]uint32{0xffffff, 1}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := xproto.MapWindowChecked(conn, win).Check(); err != nil {
		t.Fatal(err)
	}
	gc, err := xproto.NewGcontextId(conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateGCChecked(conn, gc, xproto.Drawable(win), xproto.GcForeground, []uint32{0}).Check(); err != nil {
		t.Fatal(err)
	}
	paint := func(pixel uint32, rects ...xproto.Rectangle) {
		t.Helper()
		if err := xproto.ChangeGCChecked(conn, gc, xproto.GcForeground, []uint32{pixel}).Check(); err != nil {
			t.Fatal(err)
		}
		if err := xproto.PolyFillRectangleChecked(conn, xproto.Drawable(win), gc, rects).Check(); err != nil {
			t.Fatal(err)
		}
		conn.Sync()
	}
	t.Run("real-framebuffer-templates", func(t *testing.T) {
		paint(0xff0000, xproto.Rectangle{X: 12, Y: 150, Width: 10, Height: 10}, xproto.Rectangle{X: 42, Y: 150, Width: 10, Height: 10})
		template := image.NewRGBA(image.Rect(0, 0, 10, 10))
		for y := 0; y < 10; y++ {
			for x := 0; x < 10; x++ {
				template.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
			}
		}
		area := image.Rect(40, 180, 110, 210)
		match, err := s.pf.Screen().FindTemplate(ctx, area, template, 1)
		first, second := image.Rect(52, 190, 62, 200), image.Rect(82, 190, 92, 200)
		if err != nil || match.Rect != first || match.Confidence != 1 {
			t.Fatalf("best match = %+v, %v; want %v", match, err, first)
		}
		matches, err := s.pf.Screen().FindTemplates(ctx, area, template, 1, 4)
		if err != nil || len(matches) != 2 || matches[0].Rect != first || matches[1].Rect != second {
			t.Fatalf("ordered matches = %+v, %v; want %v, %v", matches, err, first, second)
		}
	})
	binary := filepath.Join(t.TempDir(), "pf")
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", binary, "../cmd/pf")
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pf: %v: %s", err, out)
	}
	t.Run("real-screen-ocr", func(t *testing.T) {
		glyphs := map[rune][]string{
			'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
			'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
			'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
			'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
		}
		var rects []xproto.Rectangle
		for i, letter := range []rune("HELLO") {
			for y, row := range glyphs[letter] {
				for x, bit := range row {
					if bit == '1' {
						rects = append(rects, xproto.Rectangle{X: int16(12 + i*84 + x*12), Y: int16(24 + y*12), Width: 12, Height: 12})
					}
				}
			}
		}
		paint(0, rects...)
		cmd := exec.CommandContext(ctx, binary, "find", "text", "--query", "HELLO", "--rect", "40,40,460,180", "--provider", "tesseract", "--output", "json")
		cmd.Env = s.rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("real OCR CLI: %v: %s", err, stderr.String())
		}
		var got find.TextResult
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Matches) != 1 || got.Matches[0].Text != "HELLO" || got.Matches[0].Rect.Empty() ||
			!got.Matches[0].Rect.In(image.Rect(40, 40, 460, 180)) || got.Matches[0].Confidence == nil {
			t.Fatalf("unexpected real OCR result %+v", got)
		}
	})
	t.Run("cross-api-visual-find", func(t *testing.T) { certifyReleaseFindCLIAndGoOCR(t, ctx, s, binary) })
	t.Run("screen-and-find-command-contracts", func(t *testing.T) { certifyReleaseScreenCommands(t, ctx, s, binary) })
	t.Run("real-pointer-observation", func(t *testing.T) {
		if err := s.pf.Input().MouseMove(ctx, 150, 150); err != nil {
			t.Fatal(err)
		}
		want, err := xproto.QueryPointer(conn, root.Root).Reply()
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.pf.Input().ObservePointer(ctx)
		if err != nil || got.X != int(want.RootX) || got.Y != int(want.RootY) || got.X != 150 || got.Y != 150 ||
			got.CoordinateSpace.Kind != input.PointerCoordinateSpaceRootPixels || got.Provider != "x11-root" || got.ObservedAt.IsZero() {
			t.Fatalf("pointer = %+v, %v; native readback = %+v", got, err, want)
		}
	})
	t.Run("independent-cli-input-state", func(t *testing.T) {
		if err := xtest.Init(conn); err != nil {
			t.Fatal(err)
		}
		for _, transition := range []struct {
			kind  byte
			state input.StateValue
		}{{xproto.ButtonPress, input.StatePressed}, {xproto.ButtonRelease, input.StateReleased}} {
			if err := xtest.FakeInputChecked(conn, transition.kind, 1, xproto.TimeCurrentTime, root.Root, 0, 0, 0).Check(); err != nil {
				t.Fatal(err)
			}
			if transition.kind == xproto.ButtonPress {
				defer func() {
					_ = xtest.FakeInputChecked(conn, xproto.ButtonRelease, 1, xproto.TimeCurrentTime, root.Root, 0, 0, 0).Check()
				}()
			}
			cmd := exec.CommandContext(ctx, binary, "input", "state", "--button", "left", "--output", "json")
			cmd.Env = s.rt.EnvList()
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("CLI state: %v", err)
			}
			var got input.StateObservation
			if err := json.Unmarshal(out, &got); err != nil || got.State != transition.state || got.Provider != "x11-core" || got.Provenance != "unknown" || got.ObservedAt.IsZero() {
				t.Fatalf("independently injected button: %+v, %v", got, err)
			}
		}
		cmd := exec.CommandContext(ctx, binary, "input", "location", "--output", "json")
		cmd.Env = s.rt.EnvList()
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		var got input.PointerObservation
		if err := json.Unmarshal(out, &got); err != nil || got.X != 150 || got.Y != 150 || got.Provider != "x11-root" || got.CoordinateSpace.Kind != input.PointerCoordinateSpaceRootPixels {
			t.Fatalf("CLI pointer: %+v, %v", got, err)
		}
	})
	t.Run("native-input-command-events", func(t *testing.T) { certifyReleaseX11InputCommands(t, ctx, s, binary, conn, win, root.Root) })
	t.Run("unsupported-timed-motion-fails-before-dispatch", func(t *testing.T) {
		if err := s.pf.Input().MouseMove(ctx, 150, 150); err != nil {
			t.Fatal(err)
		}
		start, end := image.Pt(150, 150), image.Pt(280, 230)
		opts := perfuncted.MotionOptions{Duration: 300 * time.Millisecond}
		if err := s.pf.Input().MouseMoveSmooth(ctx, &start, end, opts); !errors.Is(err, perfuncted.ErrUnsupported) {
			t.Fatalf("timed X11 move must reject unproved coordinate metadata: %v", err)
		}
		if err := s.pf.Input().DragAndDropSmooth(ctx, start, end, opts); !errors.Is(err, perfuncted.ErrUnsupported) {
			t.Fatalf("timed X11 drag must reject unproved coordinate metadata: %v", err)
		}
		got, err := xproto.QueryPointer(conn, root.Root).Reply()
		if err != nil || int(got.RootX) != start.X || int(got.RootY) != start.Y || got.Mask&xproto.KeyButMaskButton1 != 0 {
			t.Fatalf("rejected motion dispatched pointer input: %+v, %v", got, err)
		}
	})
	t.Run("real-screen-delta", func(t *testing.T) {
		watchCtx, stop := context.WithTimeout(ctx, 12*time.Second)
		defer stop()
		cmd := exec.CommandContext(watchCtx, binary, "screen", "watch", "--changes", "--include-pixels", "--output", "json", "--rect", "240,180,320,220", "--poll", "50ms")
		cmd.Env = s.rt.EnvList()
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { stop(); _ = cmd.Wait() }()
		type frame struct {
			ID     uint64            `json:"frame_id"`
			Base   *uint64           `json:"base_frame_id"`
			Full   bool              `json:"full_frame"`
			Dirty  []image.Rectangle `json:"dirty_rects"`
			Pixels []struct {
				Data   []byte          `json:"data"`
				Bounds image.Rectangle `json:"image_bounds"`
			} `json:"pixels"`
		}
		decoder := json.NewDecoder(stdout)
		var first, delta frame
		if err := decoder.Decode(&first); err != nil || !first.Full || first.ID != 1 {
			t.Fatalf("initial frame = %+v, %v", first, err)
		}
		paint(0x0000ff, xproto.Rectangle{X: 211, Y: 150, Width: 9, Height: 7})
		if err := decoder.Decode(&delta); err != nil {
			t.Fatal(fmt.Errorf("read changed frame: %w", err))
		}
		want := image.Rect(251, 190, 260, 197)
		if delta.Full || delta.ID != 2 || delta.Base == nil || *delta.Base != first.ID || len(delta.Dirty) != 1 ||
			delta.Dirty[0] != want || len(delta.Pixels) != 1 || delta.Pixels[0].Bounds != image.Rect(11, 10, 20, 17) {
			t.Fatalf("delta does not describe exact independent paint: %+v; want %v", delta, want)
		}
		img, err := png.Decode(bytes.NewReader(delta.Pixels[0].Data))
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < 7; y++ {
			for x := 0; x < 9; x++ {
				if color.RGBAModel.Convert(img.At(x, y)) != (color.RGBA{B: 255, A: 255}) {
					t.Fatalf("delta pixel %d,%d is not blue", x, y)
				}
			}
		}
	})
}
