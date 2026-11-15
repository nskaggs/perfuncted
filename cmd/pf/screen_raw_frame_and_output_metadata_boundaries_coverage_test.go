package main

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"
)

// The raw RGBA writer is a public CLI output boundary. Validate the complete
// image layout before streaming any pixels, even when a screenshot provider
// bypasses higher-level image validation.
func TestScreenRawWriterRejectsMissingAndTruncatedFramesBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame *image.RGBA
		want  string
	}{
		{"missing image", nil, "nil capture"},
		{"truncated backing buffer", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 8)}, "invalid RGBA frame stride"},
		{"invalid negative stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: -8, Pix: make([]byte, 16)}, "invalid RGBA frame stride"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := writePackedRGBAPixels(&stdout, tc.frame)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("malformed frame accepted: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("malformed frame leaked %d bytes", stdout.Len())
			}
		})
	}
}

// No output metadata is not evidence of a particular monitor resolution.
// The caller must continue to the authorized screenshot fallback.
func TestScreenOutputResolutionProbeWithoutBundleMustFallBack(t *testing.T) {
	w, h, ok := singleOutputPhysicalResolution(context.Background(), nil)
	if ok || w != 0 || h != 0 {
		t.Fatalf("missing output bundle fabricated geometry: %dx%d ok=%t", w, h, ok)
	}
}
