package screen

import (
	"context"
	"github.com/nskaggs/perfuncted/v2/internal/capturebench"
	"image"
	"image/png"
	"io"
	"testing"
)

// BenchmarkCapturePNGEncode measures optional caller work on an in-memory image
// fixture. It is not a screenshot latency benchmark.
func BenchmarkCapturePNGEncode(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 1024, 768))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := png.Encode(io.Discard, img); err != nil {
			b.Fatal(err)
		}
	}
}

type captureBenchmarkStub struct {
	grab  func(context.Context, image.Rectangle) (image.Image, error)
	grabs int
}

func (s *captureBenchmarkStub) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.grab(ctx, rect)
}
func (*captureBenchmarkStub) GrabFullHash(context.Context) (uint32, error) {
	panic("hash path must not be used")
}
func (*captureBenchmarkStub) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	panic("hash path must not be used")
}
func (*captureBenchmarkStub) Close() error { return nil }

func TestCaptureBenchmarkUsesGrabForFullAndRegion(t *testing.T) {
	region := image.Rect(3, 4, 11, 12)
	stub := &captureBenchmarkStub{grab: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
		switch requested {
		case image.Rectangle{}:
			return image.NewRGBA(image.Rect(0, 0, 1024, 768)), nil
		case region:
			return image.NewRGBA(region), nil
		default:
			t.Fatalf("unexpected Grab rectangle %v", requested)
			return nil, nil
		}
	}}
	for _, rect := range []image.Rectangle{{}, region} {
		durations, bounds, err := capturebench.Collect(context.Background(), stub, rect, 1, 1_000_000_000)
		if err != nil || len(durations) != 1 {
			t.Fatalf("rect=%v durations=%d err=%v", rect, len(durations), err)
		}
		if rect == (image.Rectangle{}) && bounds != image.Rect(0, 0, 1024, 768) {
			t.Fatalf("full capture bounds=%v", bounds)
		}
		if rect == region && bounds != region {
			t.Fatalf("region capture bounds=%v", bounds)
		}
	}
	if stub.grabs != 2 {
		t.Fatalf("Grab calls=%d want 2", stub.grabs)
	}
}

func TestCaptureBufferSizeRejectsInvalidGeometry(t *testing.T) {
	tests := []struct {
		name     string
		width    uint32
		height   uint32
		stride   uint32
		wantErr  bool
		wantSize int
	}{
		{name: "zero width", height: 1, stride: 4, wantErr: true},
		{name: "zero height", width: 1, stride: 4, wantErr: true},
		{name: "short stride", width: 2, height: 1, stride: 4, wantErr: true},
		{name: "valid padded rows", width: 2, height: 3, stride: 12, wantSize: 36},
		{name: "overflow", width: ^uint32(0), height: ^uint32(0), stride: ^uint32(0), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := captureBufferSize(tt.width, tt.height, tt.stride)
			if (err != nil) != tt.wantErr {
				t.Fatalf("captureBufferSize error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.wantSize {
				t.Fatalf("captureBufferSize = %d, want %d", got, tt.wantSize)
			}
		})
	}
}

func TestApplyExtSessionEventRejectsShortPayloads(t *testing.T) {
	var info extSessionInfo
	var stopped, invalid bool
	applyExtSessionEvent(&info, &stopped, &invalid, 0, make([]byte, 4))
	if !invalid {
		t.Fatal("short buffer_size payload was accepted")
	}

	invalid = false
	applyExtSessionEvent(&info, &stopped, &invalid, 1, nil)
	if !invalid {
		t.Fatal("short shm_format payload was accepted")
	}
}

func TestWlrFramePayloadRejectsShortBufferEvent(t *testing.T) {
	var ready, failed, bufDone bool
	var info bufInfo
	applyWlrFrameEvent(&info, &ready, &failed, &bufDone, 0, make([]byte, 8))
	if !failed {
		t.Fatal("short buffer payload was accepted")
	}
}

// A nil concrete image stored in an interface must remain a failed capture,
// not a successful frame or a nil pointer panic during Bounds().
func TestRequireCapturedImageRejectsTypedNilAndEmptyFrames(t *testing.T) {
	var rgba *image.RGBA
	var nrgba *image.NRGBA
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"typed nil RGBA", rgba},
		{"typed nil NRGBA", nrgba},
		{"empty RGBA", image.NewRGBA(image.Rectangle{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requireCapturedImage("test", tc.img)
			if err == nil || got != nil {
				t.Fatalf("accepted invalid image: image=%v error=%v", got, err)
			}
		})
	}
}
