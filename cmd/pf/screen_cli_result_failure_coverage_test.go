package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type screenCommandFailureWriter struct {
	bytes.Buffer
	failAt  int
	failure error
	writes  int
}

func (w *screenCommandFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.failure
	}
	return w.Buffer.Write(p)
}

func runScreenCommandWithCapture(t *testing.T, capture *pftest.Screenshotter, out io.Writer, args ...string) error {
	t.Helper()
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(capture, nil, nil, nil), nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func TestScreenCommandsPreserveCaptureAndEncodingFailures(t *testing.T) {
	failure := errors.New("synthetic screenshot source failure")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"stdout grab", []string{"grab", "--out", "-"}},
		{"file grab", []string{"grab", "--out", t.TempDir() + "/capture.png"}},
		{"wait for predicate", []string{"wait-for-fn"}},
		{"wait for settle", []string{"wait-for-settle"}},
		{"multiple pixels", []string{"get-multiple-pixels", "--points", "0,0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runScreenCommandWithCapture(t, &pftest.Screenshotter{Err: failure}, io.Discard, tc.args...)
			if !errors.Is(err, failure) {
				t.Fatalf("screen command error = %v, want source failure", err)
			}
		})
	}
}

func TestGetAllPixelsRejectsUnexpectedCaptureTypeWithoutOutput(t *testing.T) {
	frame := image.NewGray(image.Rect(0, 0, 1, 1))
	var out bytes.Buffer
	err := runScreenCommandWithCapture(t, &pftest.Screenshotter{Frames: []image.Image{frame}}, &out, "get-all-pixels")
	if err == nil || !strings.Contains(err.Error(), "unexpected image type *image.Gray") {
		t.Fatalf("get-all-pixels error = %v, want concrete image type rejection", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected capture type emitted %d bytes", out.Len())
	}
}

func TestGetMultiplePixelsPreservesPlainAndJSONWriterFailures(t *testing.T) {
	failure := errors.New("synthetic output consumer failure")
	for _, tc := range []struct {
		name   string
		format string
	}{
		{"plain", "plain"},
		{"json", "json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &screenCommandFailureWriter{failAt: 1, failure: failure}
			err := runScreenCommandWithCapture(
				t,
				&pftest.Screenshotter{Width: 1, Height: 1},
				out,
				"get-multiple-pixels", "--points", "0,0", "--output", tc.format,
			)
			if !errors.Is(err, failure) || out.writes != 1 {
				t.Fatalf("screen output error=%v writes=%d, want one propagated write failure", err, out.writes)
			}
		})
	}
}

func TestScreenWatchPropagatesBackendAndOutputFailures(t *testing.T) {
	failure := errors.New("synthetic screen watch failure")
	t.Run("backend", func(t *testing.T) {
		err := runScreenCommandWithCapture(
			t,
			&pftest.Screenshotter{Err: failure},
			io.Discard,
			"watch", "--poll", "1ms", "--duration", "1s",
		)
		if !errors.Is(err, failure) {
			t.Fatalf("screen watch error = %v, want backend failure", err)
		}
	})

	for _, tc := range []struct {
		name   string
		format string
		failAt int
	}{
		{"initial plain result", "plain", 1},
		{"initial JSON result", "json", 1},
		{"changed plain result", "plain", 2},
		{"changed JSON result", "json", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &screenCommandFailureWriter{failAt: tc.failAt, failure: failure}
			frames := []image.Image{
				solidScreenCommandFrame(color.RGBA{A: 255}),
				solidScreenCommandFrame(color.RGBA{R: 255, A: 255}),
			}
			err := runScreenCommandWithCapture(
				t,
				&pftest.Screenshotter{Frames: frames},
				out,
				"watch", "--poll", "1ms", "--duration", "1s", "--output", tc.format,
			)
			if !errors.Is(err, failure) || out.writes != tc.failAt {
				t.Fatalf("screen watch output error=%v writes=%d, want failure at write %d", err, out.writes, tc.failAt)
			}
		})
	}
}

func solidScreenCommandFrame(c color.RGBA) image.Image {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, c)
	return frame
}
