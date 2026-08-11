package screen

import "testing"

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
