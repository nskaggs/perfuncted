//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
)

func TestReleaseVisualAcceptanceWayland(t *testing.T) {
	s := mustSuite(t)
	if s.mode != displayHeadlessWayland && s.mode != displayNestedWayland {
		t.Fatal("requires isolated headless or nested Wayland")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	w, h, err := s.pf.Screen.Resolution(ctx)
	if err != nil || w < 480 || h < 320 {
		t.Fatalf("fixture requires at least 480x320: %dx%d, %v", w, h, err)
	}
	if err := s.pf.Input.MouseMove(ctx, w-40, h-40); err != nil {
		t.Fatal(err)
	}
	base := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(base, base.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	red := color.RGBA{R: 255, A: 255}
	a, b := image.Rect(52, 190, 62, 200), image.Rect(82, 190, 92, 200)
	for _, r := range []image.Rectangle{a, b} {
		draw.Draw(base, r, image.NewUniform(red), image.Point{}, draw.Src)
	}
	paintReleaseGlyphs(base)
	setReleaseWallpaper(t, ctx, s, base)
	area := image.Rect(40, 40, 460, 210)
	waitReleasePixels(t, ctx, s, area, base)
	t.Run("real-framebuffer-templates", func(t *testing.T) {
		template := image.NewRGBA(image.Rect(0, 0, 10, 10))
		draw.Draw(template, template.Bounds(), image.NewUniform(red), image.Point{}, draw.Src)
		region := image.Rect(40, 180, 110, 210)
		got, err := s.pf.Screen.FindTemplate(ctx, region, template, 1)
		if err != nil || got.Rect != a || got.Confidence != 1 {
			t.Fatalf("best template: %+v, %v", got, err)
		}
		all, err := s.pf.Screen.FindTemplates(ctx, region, template, 1, 4)
		if err != nil || len(all) != 2 || all[0].Rect != a || all[1].Rect != b {
			t.Fatalf("ordered templates: %+v, %v", all, err)
		}
	})
	binary := filepath.Join(t.TempDir(), "pf")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/pf")
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pf: %v: %s", err, out)
	}
	t.Run("real-screen-ocr", func(t *testing.T) {
		region := image.Rect(40, 40, 460, 180)
		cmd := exec.CommandContext(ctx, binary, "find", "text", "--query", "HELLO", "--rect", "40,40,460,180", "--provider", "tesseract", "--output", "json")
		cmd.Env = s.rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("OCR: %v: %s", err, stderr.String())
		}
		var got find.TextResult
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Matches) != 1 || got.Matches[0].Text != "HELLO" || got.Matches[0].Rect.Empty() || !got.Matches[0].Rect.In(region) || got.Matches[0].Confidence == nil {
			t.Fatalf("unexpected OCR: %+v", got)
		}
	})
	t.Run("cross-api-visual-find", func(t *testing.T) { certifyReleaseFindCLIAndGoOCR(t, ctx, s, binary) })
	t.Run("screen-and-find-command-contracts", func(t *testing.T) { certifyReleaseScreenCommands(t, ctx, s, binary) })
	t.Run("real-screen-stream", func(t *testing.T) {
		certifyWaylandStream(t, ctx, s, binary, base)
	})
	t.Run("native-timed-motion-and-drag", func(t *testing.T) {
		certifyWaylandMotion(t, ctx, s, binary)
	})
	t.Run("unsupported-global-observation", func(t *testing.T) {
		for _, args := range [][]string{{"input", "location", "--output", "json"}, {"input", "state", "--button", "left"}, {"input", "hotkey", "listen", "--button", "left", "--timeout", "500ms"}} {
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = s.rt.EnvList()
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			out, err := cmd.Output()
			if err == nil || len(out) != 0 || !strings.Contains(strings.ToLower(diagnostic.String()), "unsupported") {
				t.Fatalf("unsupported command %v: stdout=%q stderr=%q err=%v", args, out, diagnostic.String(), err)
			}
		}
	})
}

func paintReleaseGlyphs(img *image.RGBA) {
	glyphs := map[rune][]string{
		'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
		'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
		'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
		'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	}
	for i, letter := range []rune("HELLO") {
		for y, row := range glyphs[letter] {
			for x, bit := range row {
				if bit == '1' {
					p := image.Pt(52+i*84+x*12, 64+y*12)
					draw.Draw(img, image.Rectangle{Min: p, Max: p.Add(image.Pt(12, 12))}, image.NewUniform(color.Black), image.Point{}, draw.Src)
				}
			}
		}
	}
}

func setReleaseWallpaper(t *testing.T, ctx context.Context, s *suite, img image.Image) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(f, img)
	if err := errors.Join(encodeErr, f.Close()); err != nil {
		t.Fatal(err)
	}
	sockets, err := filepath.Glob(filepath.Join(s.rt.Get("XDG_RUNTIME_DIR"), "sway-ipc.*.sock"))
	if err != nil || len(sockets) != 1 {
		t.Fatalf("private Sway IPC: %v, %v", sockets, err)
	}
	cmd := exec.CommandContext(ctx, "swaymsg", "-s", sockets[0], "output", "*", "bg", path, "stretch")
	cmd.Env = s.rt.EnvList()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("paint independent wallpaper: %v: %s", err, out)
	}
}

func releasePixelsEqual(got image.Image, want image.Image, region image.Rectangle) bool {
	if got == nil || got.Bounds().Size() != region.Size() {
		return false
	}
	for y := 0; y < region.Dy(); y++ {
		for x := 0; x < region.Dx(); x++ {
			p := image.Pt(x, y)
			if color.RGBAModel.Convert(got.At(got.Bounds().Min.X+p.X, got.Bounds().Min.Y+p.Y)) != color.RGBAModel.Convert(want.At(region.Min.X+p.X, region.Min.Y+p.Y)) {
				return false
			}
		}
	}
	return true
}

func waitReleasePixels(t *testing.T, ctx context.Context, s *suite, region image.Rectangle, want image.Image) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.pf.Screen.Grab(ctx, region)
		if err != nil {
			t.Fatal(err)
		}
		if releasePixelsEqual(got, want, region) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("independently rendered pixels did not appear in %v", region)
}

type releaseWireFrame struct {
	ID      uint64            `json:"frame_id"`
	Version uint64            `json:"geometry_version"`
	Base    *uint64           `json:"base_frame_id"`
	Full    bool              `json:"full_frame"`
	Screen  image.Rectangle   `json:"screen_rect"`
	Bounds  image.Rectangle   `json:"image_bounds"`
	Dirty   []image.Rectangle `json:"dirty_rects"`
	Pixels  []struct {
		Bounds   image.Rectangle `json:"image_bounds"`
		Encoding string          `json:"encoding"`
		Data     []byte          `json:"data"`
	} `json:"pixels"`
}

func certifyWaylandStream(t *testing.T, ctx context.Context, s *suite, binary string, base *image.RGBA) {
	t.Helper()
	region := image.Rect(240, 180, 320, 220)
	watchCtx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	cmd := exec.CommandContext(watchCtx, binary, "screen", "watch", "--changes", "--include-pixels", "--output", "json", "--rect", "240,180,320,220", "--poll", "50ms")
	cmd.Env = s.rt.EnvList()
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stop(); _ = cmd.Wait() }()
	decoder := json.NewDecoder(out)
	var first releaseWireFrame
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if !first.Full || first.Base != nil || first.ID != 1 {
		t.Fatalf("initial frame: %+v", first)
	}
	reconstructed := applyWaylandFrame(t, nil, first, region, 0)
	if !releasePixelsEqual(reconstructed, base, region) {
		t.Fatal("initial reconstructed frame differs from independent fixture")
	}
	next := image.NewRGBA(base.Bounds())
	draw.Draw(next, next.Bounds(), base, base.Bounds().Min, draw.Src)
	patch := image.Rect(251, 190, 260, 197)
	draw.Draw(next, patch, image.NewUniform(color.RGBA{B: 255, A: 255}), image.Point{}, draw.Src)
	setReleaseWallpaper(t, ctx, s, next)
	// A compositor may briefly show its old background during replacement.
	// Validate every wire record, and require exact final independent pixels.
	id := first.ID
	for {
		var frame releaseWireFrame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatalf("read changed Wayland frame: %v", err)
		}
		reconstructed = applyWaylandFrame(t, reconstructed, frame, region, id)
		id = frame.ID
		if releasePixelsEqual(reconstructed, next, region) {
			if !frame.Full && len(frame.Dirty) == 1 && frame.Dirty[0] == patch {
				t.Log("exact dirty-region delta observed")
			} else {
				t.Logf("compositor replacement required full/intermediate resync: full=%t dirty=%v", frame.Full, frame.Dirty)
			}
			break
		}
	}
}

func applyWaylandFrame(t *testing.T, previous *image.RGBA, frame releaseWireFrame, region image.Rectangle, id uint64) *image.RGBA {
	t.Helper()
	if frame.ID != id+1 || frame.Version != 1 || frame.Screen != region || frame.Bounds.Size() != region.Size() || len(frame.Pixels) == 0 || len(frame.Pixels) != len(frame.Dirty) {
		t.Fatalf("invalid frame header: %+v", frame)
	}
	if frame.Full {
		if frame.Base != nil || len(frame.Pixels) != 1 || frame.Pixels[0].Bounds != frame.Bounds || frame.Dirty[0] != region {
			t.Fatal("bad full resync")
		}
		previous = image.NewRGBA(frame.Bounds)
	} else if previous == nil || frame.Base == nil || *frame.Base != id || previous.Bounds() != frame.Bounds {
		t.Fatal("bad delta base")
	}
	for i, p := range frame.Pixels {
		if p.Encoding != "png+base64" || p.Bounds.Empty() || !p.Bounds.In(frame.Bounds) || p.Bounds.Sub(frame.Bounds.Min).Add(region.Min) != frame.Dirty[i] {
			t.Fatal("bad pixel geometry")
		}
		img, err := png.Decode(bytes.NewReader(p.Data))
		if err != nil || img.Bounds().Size() != p.Bounds.Size() {
			t.Fatalf("bad PNG: %v", err)
		}
		draw.Draw(previous, p.Bounds, img, img.Bounds().Min, draw.Src)
	}
	return previous
}

var releaseMotionPattern = regexp.MustCompile(`motion:.*x, y: ([0-9.]+), ([0-9.]+)`)

func certifyWaylandMotion(t *testing.T, ctx context.Context, s *suite, binary string) {
	t.Helper()
	space, err := s.pf.Input.PointerCoordinateSpace(ctx)
	if err != nil || space.Kind != input.CoordinateSpaceLogical || space.ScaleX != 1 || space.ScaleY != 1 {
		t.Fatalf("Wayland motion coordinate metadata: %+v, %v", space, err)
	}
	log, err := os.Create(filepath.Join(t.TempDir(), "wev.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.CommandContext(ctx, "stdbuf", "-oL", "-eL", "wev", "-f", "wl_pointer")
	cmd.Env = s.rt.EnvList()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	win, err := s.pf.Windows.Wait(ctx, perfuncted.WindowMatch{TitleContains: "wev"}, perfuncted.WaitEvery(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	info, err := win.Info(ctx)
	if err != nil || info.W < 320 || info.H < 250 {
		t.Fatalf("wev geometry: %+v, %v", info, err)
	}
	origin := image.Pt(info.X, info.Y)
	start := origin.Add(image.Pt(60, 60))
	end := origin.Add(image.Pt(260, 180))
	if err := s.pf.Input.MouseMove(ctx, start.X, start.Y); err != nil {
		t.Fatal(err)
	}
	// Window discovery can precede Wayland surface commit and pointer focus.
	// Repeated native motion establishes readiness, rather than assuming
	// that a successful injection call implies delivery to the client.
	if err := win.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	waitReleasePointerFocus(t, ctx, s, log.Name(), start)
	offset := releaseLogSize(t, log.Name())
	began := time.Now()
	opts := perfuncted.MotionOptions{Duration: 450 * time.Millisecond, Easing: "linear"}
	if err := s.pf.Input.MouseMoveSmooth(ctx, &start, end, opts); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed < 400*time.Millisecond {
		t.Fatalf("timed move returned early: %v", elapsed)
	}
	verifyReleaseMotion(t, log.Name(), offset, end.Sub(origin), false)
	offset = releaseLogSize(t, log.Name())
	if err := s.pf.Input.DragAndDropSmooth(ctx, end, start, opts); err != nil {
		t.Fatal(err)
	}
	verifyReleaseMotion(t, log.Name(), offset, start.Sub(origin), true)
	offset = releaseLogSize(t, log.Name())
	cli := exec.CommandContext(ctx, binary, "input", "move", "--from-x", strconv.Itoa(start.X), "--from-y", strconv.Itoa(start.Y), "--x", strconv.Itoa(end.X), "--y", strconv.Itoa(end.Y), "--duration", "450ms", "--easing", "ease-in-out", "--curve", "0.2", "--variation", "2", "--seed", "42")
	cli.Env = s.rt.EnvList()
	if out, err := cli.CombinedOutput(); err != nil {
		t.Fatalf("CLI timed move: %v: %s", err, out)
	}
	verifyReleaseMotion(t, log.Name(), offset, end.Sub(origin), false)
	offset = releaseLogSize(t, log.Name())
	cli = exec.CommandContext(ctx, binary, "input", "drag-and-drop", "--x1", strconv.Itoa(end.X), "--y1", strconv.Itoa(end.Y), "--x2", strconv.Itoa(start.X), "--y2", strconv.Itoa(start.Y), "--duration", "450ms")
	cli.Env = s.rt.EnvList()
	if out, err := cli.CombinedOutput(); err != nil {
		t.Fatalf("CLI timed drag: %v: %s", err, out)
	}
	verifyReleaseMotion(t, log.Name(), offset, start.Sub(origin), true)
	offset = releaseLogSize(t, log.Name())
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- s.pf.Input.DragAndDropSmooth(cancelCtx, start, end, perfuncted.MotionOptions{Duration: 2 * time.Second})
	}()
	waitReleaseLog(t, log.Name(), func(s string) bool { return strings.Contains(s[offset:], "state: 1 (pressed)") })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled drag: %v", err)
	}
	waitReleaseLog(t, log.Name(), func(s string) bool { return strings.Contains(s[offset:], "state: 0 (released)") })
}

func waitReleasePointerFocus(t *testing.T, ctx context.Context, s *suite, path string, start image.Point) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		if err := s.pf.Input.MouseMove(ctx, start.X+i%2, start.Y); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "enter:") || strings.Contains(string(data), "motion:") {
			return
		}
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("native Wayland client never acquired pointer focus: %s", data)
}

func releaseLogSize(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}
func waitReleaseLog(t *testing.T, path string, predicate func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		last = string(b)
		if predicate(last) {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("native pointer events missing: %s", last)
	return ""
}
func verifyReleaseMotion(t *testing.T, path string, offset int, end image.Point, drag bool) {
	t.Helper()
	waitReleaseLog(t, path, func(log string) bool {
		part := log[offset:]
		matches := releaseMotionPattern.FindAllStringSubmatch(part, -1)
		if len(matches) < 3 {
			return false
		}
		last := matches[len(matches)-1]
		x, xerr := strconv.ParseFloat(last[1], 64)
		y, yerr := strconv.ParseFloat(last[2], 64)
		if xerr != nil || yerr != nil || absRelease(x-float64(end.X)) > 2 || absRelease(y-float64(end.Y)) > 2 {
			return false
		}
		if drag {
			press := strings.Index(part, "state: 1 (pressed)")
			release := strings.LastIndex(part, "state: 0 (released)")
			return press >= 0 && release > press && strings.Contains(part[press:release], "motion:")
		}
		return !strings.Contains(part, "button:")
	})
}
func absRelease(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
