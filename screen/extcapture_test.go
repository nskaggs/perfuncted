package screen

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type extCaptureTestCtx struct {
	nextID uint32
	msgs   [][]byte
}

func (c *extCaptureTestCtx) Register(p wl.Proxy) {
	c.nextID++
	p.SetID(c.nextID)
	p.SetCtx(c)
}

func (c *extCaptureTestCtx) SetProxy(id uint32, p wl.Proxy) {
	p.SetID(id)
	p.SetCtx(c)
}

func (c *extCaptureTestCtx) WriteMsg(data, _ []byte) error {
	msg := make([]byte, len(data))
	copy(msg, data)
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *extCaptureTestCtx) Dispatch() error { return nil }
func (c *extCaptureTestCtx) Close() error    { return nil }

func TestExtCaptureAvailabilityRequiresOutputSourceManager(t *testing.T) {
	t.Run("missing copy manager", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_output_image_capture_source_manager_v1": true,
		})
		if ok {
			t.Fatal("extCaptureAvailable() = true, want false")
		}
		if reason != "ext_image_copy_capture_manager_v1 not advertised" {
			t.Fatalf("reason = %q", reason)
		}
	})

	t.Run("missing output source manager", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_image_copy_capture_manager_v1": true,
		})
		if ok {
			t.Fatal("extCaptureAvailable() = true, want false")
		}
		if reason != "ext_output_image_capture_source_manager_v1 not advertised" {
			t.Fatalf("reason = %q", reason)
		}
	})

	t.Run("full protocol stack", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_image_copy_capture_manager_v1":          true,
			"ext_output_image_capture_source_manager_v1": true,
		})
		if !ok {
			t.Fatal("extCaptureAvailable() = false, want true")
		}
		if reason == "" {
			t.Fatal("expected non-empty reason")
		}
	})
}

func TestExtCaptureProtocolOpcodes(t *testing.T) {
	ctx := &extCaptureTestCtx{}

	if err := sendExtOutputCreateSource(ctx, 10, 20, 30); err != nil {
		t.Fatalf("sendExtOutputCreateSource: %v", err)
	}
	if err := sendExtCreateSession(ctx, 11, 21, 31); err != nil {
		t.Fatalf("sendExtCreateSession: %v", err)
	}
	if err := sendExtCreateFrame(ctx, 12, 22); err != nil {
		t.Fatalf("sendExtCreateFrame: %v", err)
	}
	if err := sendExtAttachBuffer(ctx, 13, 23); err != nil {
		t.Fatalf("sendExtAttachBuffer: %v", err)
	}
	if err := sendExtDamageBuffer(ctx, 14, 640, 480); err != nil {
		t.Fatalf("sendExtDamageBuffer: %v", err)
	}
	if err := sendExtCapture(ctx, 15); err != nil {
		t.Fatalf("sendExtCapture: %v", err)
	}

	tests := []struct {
		name      string
		msg       []byte
		senderID  uint32
		opcode    uint32
		bodyWords []uint32
	}{
		{"create source", ctx.msgs[0], 10, 0, []uint32{20, 30}},
		{"create session", ctx.msgs[1], 11, 0, []uint32{21, 31, 0}},
		{"create frame", ctx.msgs[2], 12, 0, []uint32{22}},
		{"attach buffer", ctx.msgs[3], 13, 1, []uint32{23}},
		{"damage buffer", ctx.msgs[4], 14, 2, []uint32{0, 0, 640, 480}},
		{"capture", ctx.msgs[5], 15, 3, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := wl.Uint32(tc.msg[0:4]); got != tc.senderID {
				t.Fatalf("sender = %d, want %d", got, tc.senderID)
			}
			sizeOpcode := wl.Uint32(tc.msg[4:8])
			if got := sizeOpcode & 0xffff; got != tc.opcode {
				t.Fatalf("opcode = %d, want %d", got, tc.opcode)
			}
			if got := int(sizeOpcode >> 16); got != len(tc.msg) {
				t.Fatalf("size = %d, want %d", got, len(tc.msg))
			}
			for i, want := range tc.bodyWords {
				if got := wl.Uint32(tc.msg[8+i*4:]); got != want {
					t.Fatalf("word %d = %d, want %d", i, got, want)
				}
			}
		})
	}
}
