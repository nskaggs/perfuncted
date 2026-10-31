package screen

import (
	"context"
	"strings"
	"testing"
)

// Resolution is relied on for capture bounds, cursor positioning, and ROI
// validation. A broken/disconnected resolver must not return impossible output
// sizes as an authoritative desktop dimension.
func TestResolutionWithContextRejectsNonPositiveResolverDimensions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		contextAware bool
		w, h         int
	}{
		{"context zero width", true, 0, 1080},
		{"context zero height", true, 1920, 0},
		{"context negative width", true, -1, 1080},
		{"context negative height", true, 1920, -1},
		{"classic zero width", false, 0, 1080},
		{"classic zero height", false, 1920, 0},
		{"classic negative width", false, -1, 1080},
		{"classic negative height", false, 1920, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend Screenshotter
			if tc.contextAware {
				backend = &syntheticContextResolver{width: tc.w, height: tc.h}
			} else {
				backend = &syntheticResolver{width: tc.w, height: tc.h}
			}
			w, h, err := ResolutionWithContext(context.Background(), backend)
			if err == nil || !strings.Contains(err.Error(), "invalid") || w != 0 || h != 0 {
				t.Fatalf("unusable screen %dx%d treated as valid dimensions %dx%d error=%v", tc.w, tc.h, w, h, err)
			}
		})
	}
}
