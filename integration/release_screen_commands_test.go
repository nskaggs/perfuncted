//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// All colors and coordinates below are defined by the independent fixture,
// not a prior Perfuncted screen query.
func certifyReleaseScreenCommands(t *testing.T, ctx context.Context, s *suite, binary string) {
	t.Helper()
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, stderr.String())
		}
		return out
	}
	check := func(want string, args ...string) {
		t.Helper()
		if got := strings.TrimSpace(string(run(args...))); got != want {
			t.Fatalf("pf %v = %q, want %q", args, got, want)
		}
	}
	const region = "52,190,62,200"
	red := []byte{255, 0, 0, 255}
	hash := fmt.Sprintf("%08x", crc32.ChecksumIEEE(bytes.Repeat(red, 100)))
	check("R=255 G=0 B=0", "screen", "pixel", "--x", "52", "--y", "190")
	check("52,190", "find", "color", "--color", "ff0000", "--rect", "40,180,110,210")
	check("52,190", "find", "color", "--color", "fe0101", "--tolerance", "1", "--rect", "40,180,110,210")
	var points []struct {
		X, Y       int
		R, G, B, A uint8
	}
	if err := json.Unmarshal(run("screen", "get-multiple-pixels", "--points", "52,190;45,185;52,190", "--output", "json"), &points); err != nil || len(points) != 3 {
		t.Fatalf("multiple pixels: %+v %v", points, err)
	}
	for i, p := range points {
		x, y := 52, 190
		want := color.RGBA{255, 0, 0, 255}
		if i == 1 {
			x, y = 45, 185
			want = color.RGBA{255, 255, 255, 255}
		}
		if p.X != x || p.Y != y || (color.RGBA{p.R, p.G, p.B, p.A}) != want {
			t.Fatalf("sample %d: %+v", i, p)
		}
	}
	check("52,190 R=255 G=0 B=0 A=255", "screen", "get-multiple-pixels", "--points", "52,190")
	check(hash, "screen", "hash", "--rect", region)
	img, err := png.Decode(bytes.NewReader(run("screen", "grab-region", "--rect", region, "--out", "-")))
	if err != nil || img.Bounds() != image.Rect(0, 0, 10, 10) {
		t.Fatalf("region PNG: %v", err)
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			if color.RGBAModel.Convert(img.At(x, y)) != (color.RGBA{255, 0, 0, 255}) {
				t.Fatalf("region PNG pixel %d,%d", x, y)
			}
		}
	}
	w, h, err := s.pf.Screen().WithContext(ctx).Resolution()
	if err != nil {
		t.Fatal(err)
	}
	raw := run("screen", "get-all-pixels")
	if len(raw) != w*h*4 {
		t.Fatalf("full raw image length=%d, want %d", len(raw), w*h*4)
	}
	offset := (190*w + 52) * 4
	if !bytes.Equal(raw[offset:offset+4], red) {
		t.Fatalf("full raw image sample: %v", raw[offset:offset+4])
	}
	check(fmt.Sprintf("%08x", crc32.ChecksumIEEE(raw)), "screen", "grab-full-hash")
	check(hash, "find", "wait-for", "--hash", "0x"+hash, "--rect", region, "--timeout", "2s")
	check(hash, "find", "wait-for-no-change", "--rect", region, "--stable", "3", "--poll", "20ms", "--timeout", "2s")
	check(hash, "screen", "wait-for-no-change", "--rect", region, "--stable", "3", "--poll", "20ms")
	check(hash, "screen", "wait-for-no-change-from", "--rect", region, "--initial", "0x"+hash, "--stable", "3", "--poll", "20ms")
	check(hash, "screen", "wait-for-fn", "--rect", region, "--predicate", "opaque", "--timeout", "2s")
	check("match (52,190)-(62,200) -> "+hash, "find", "scan-for", "--rects", "45,185,46,186;"+region, "--wants", "0x"+hash+",0x"+hash, "--timeout", "2s")
	// An unchanged frame must never be reported as a changed frame.
	for _, args := range [][]string{
		{"find", "wait-for-change", "--rect", region, "--initial", "0x" + hash, "--timeout", "150ms", "--poll", "20ms"},
		{"find", "wait-for-visible-change", "--rect", region, "--timeout", "150ms", "--poll", "20ms"},
		{"screen", "wait-for-settle", "--rect", region, "--timeout", "150ms", "--poll", "20ms"},
	} {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		start := time.Now()
		out, err := cmd.Output()
		if err == nil || len(out) != 0 || time.Since(start) < 100*time.Millisecond || !strings.Contains(strings.ToLower(diagnostic.String()), "deadline") {
			t.Fatalf("unchanged frame %v: out=%q err=%v diagnostic=%q", args, out, err, diagnostic.String())
		}
	}
}
