package screen

import (
	"image"
	"testing"
)

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
