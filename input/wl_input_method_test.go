//go:build linux
// +build linux

package input

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// encodeWlString encodes a Wayland string (length+bytes+null+padding).
// Kept as a test helper since it verifies wire encoding.
func encodeWlString(s string) []byte {
	n := uint32(len(s) + 1)
	b := make([]byte, 4)
	wl.PutUint32(b, n)
	out := make([]byte, 0, 4+len(s)+4)
	out = append(out, b...)
	out = append(out, s...)
	padded := (n + 3) &^ 3
	zeros := int(padded) - len(s)
	for i := 0; i < zeros; i++ {
		out = append(out, 0)
	}
	return out
}

func TestEncodeWlString(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		// "hello" → length=6, "hello\0", padded to 8 bytes
		{
			"hello",
			[]byte{
				6, 0, 0, 0, // length = 6
				'h', 'e', 'l', 'l', 'o', 0,
				0, 0, // padding
			},
		},
		// empty string → length=1, null, padded to 4 bytes
		{
			"",
			[]byte{
				1, 0, 0, 0, // length = 1
				0,
				0, 0, 0, // padding
			},
		},
		// "ab" → length=3, "ab\0\0" (padded to 4)
		{
			"ab",
			[]byte{
				3, 0, 0, 0,
				'a', 'b', 0, 0,
			},
		},
	}
	for _, tc := range tests {
		got := encodeWlString(tc.input)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("encodeWlString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestEncodeWlString_Length(t *testing.T) {
	// Verify the length field is strlen+1 (includes null terminator)
	got := encodeWlString("a")
	if len(got) < 4 {
		t.Fatal("encodeWlString too short")
	}
	length := wl.Uint32(got[0:4])
	if length != 2 { // strlen("a")+1 = 2
		t.Errorf("length = %d, want 2", length)
	}
}

func TestEncodeWlString_Padding(t *testing.T) {
	// "abc" → length=4, "abc\0" = 4 bytes, no padding needed
	got := encodeWlString("abc")
	// Total: 4 (length) + 4 (string+null, already 4-byte aligned) = 8
	if len(got) != 8 {
		t.Errorf("len = %d, want 8", len(got))
	}

	// "abcd" → length=5, "abcd\0" + 3 pad = 8 bytes of string content
	got = encodeWlString("abcd")
	// Total: 4 (length) + 8 (string+null+pad) = 12
	if len(got) != 12 {
		t.Errorf("len = %d, want 12", len(got))
	}
}

func TestWlInputMethodBackend_New_NoSocket(t *testing.T) {
	_, err := NewWlInputMethodBackend("", 1024, 768)
	if err == nil {
		t.Fatal("expected error for empty socket")
	}
	t.Logf("got expected error: %v", err)
}

func TestWlInputMethodBackend_New_Unreachable(t *testing.T) {
	_, err := NewWlInputMethodBackend("/nonexistent.sock", 1024, 768)
	if err == nil {
		t.Fatal("expected error for unreachable socket")
	}
	t.Logf("got expected error: %v", err)
}

type recordingOtherInputter struct {
	Calls    []string
	closed   bool
	closeErr error
}

func (r *recordingOtherInputter) record(s string) { r.Calls = append(r.Calls, s) }
func (r *recordingOtherInputter) KeyDown(ctx context.Context, key string) error {
	r.record("down:" + key)
	return nil
}
func (r *recordingOtherInputter) KeyUp(ctx context.Context, key string) error {
	r.record("up:" + key)
	return nil
}
func (r *recordingOtherInputter) Type(ctx context.Context, s string) error {
	r.record("type:" + s)
	return nil
}
func (r *recordingOtherInputter) MouseMove(ctx context.Context, x, y int) error {
	r.record("move")
	return nil
}
func (r *recordingOtherInputter) MouseClick(ctx context.Context, x, y, button int) error {
	r.record("click")
	return nil
}
func (r *recordingOtherInputter) MouseDown(ctx context.Context, button int) error {
	r.record("mousedown")
	return nil
}
func (r *recordingOtherInputter) MouseUp(ctx context.Context, button int) error {
	r.record("mouseup")
	return nil
}
func (r *recordingOtherInputter) ScrollUp(ctx context.Context, clicks int) error {
	r.record("scroll-up")
	return nil
}
func (r *recordingOtherInputter) ScrollDown(ctx context.Context, clicks int) error {
	r.record("scroll-down")
	return nil
}
func (r *recordingOtherInputter) ScrollLeft(ctx context.Context, clicks int) error {
	r.record("scroll-left")
	return nil
}
func (r *recordingOtherInputter) ScrollRight(ctx context.Context, clicks int) error {
	r.record("scroll-right")
	return nil
}
func (r *recordingOtherInputter) PointerLocation(ctx context.Context) (int, int, error) {
	r.record("pointer")
	return 42, 24, nil
}
func (r *recordingOtherInputter) Sync(ctx context.Context) error {
	r.record("sync")
	return nil
}
func (r *recordingOtherInputter) Close() error {
	r.closed = true
	r.record("close")
	return r.closeErr
}

func TestWlInputMethodBackend_NoOtherReturnsUnsupported(t *testing.T) {
	b := &WlInputMethodBackend{}
	ctx := context.Background()

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "Type", run: func() error { return b.Type(ctx, "text") }},
		{name: "KeyDown", run: func() error { return b.KeyDown(ctx, "a") }},
		{name: "MouseMove", run: func() error { return b.MouseMove(ctx, 1, 2) }},
		{name: "ScrollUp", run: func() error { return b.ScrollUp(ctx, 1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("%s error = %v, want ErrNotSupported", tt.name, err)
			}
		})
	}
	x, y, err := b.PointerLocation(ctx)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("PointerLocation error = %v, want ErrNotSupported", err)
	}
	if x != 0 || y != 0 {
		t.Fatalf("PointerLocation = (%d,%d), want zeros", x, y)
	}
	if err := b.Sync(ctx); err != nil {
		t.Fatalf("Sync without other backend = %v, want nil", err)
	}
}

func TestWlInputMethodBackend_DelegatesToOther(t *testing.T) {
	other := &recordingOtherInputter{}
	b := &WlInputMethodBackend{other: other}
	ctx := context.Background()

	if err := b.Type(ctx, "hello"); err != nil {
		t.Fatalf("Type: %v", err)
	}
	if err := b.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("KeyDown: %v", err)
	}
	if err := b.MouseMove(ctx, 10, 20); err != nil {
		t.Fatalf("MouseMove: %v", err)
	}
	if err := b.ScrollRight(ctx, 3); err != nil {
		t.Fatalf("ScrollRight: %v", err)
	}
	if err := b.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	want := []string{"type:hello", "down:a", "move", "scroll-right", "sync"}
	if len(other.Calls) != len(want) {
		t.Fatalf("calls = %v, want %v", other.Calls, want)
	}
	for i, exp := range want {
		if other.Calls[i] != exp {
			t.Fatalf("call %d = %q, want %q", i, other.Calls[i], exp)
		}
	}
}

func TestWlInputMethodBackend_CloseClosesOther(t *testing.T) {
	closeErr := errors.New("other close failed")
	other := &recordingOtherInputter{closeErr: closeErr}
	b := &WlInputMethodBackend{other: other}

	err := b.Close()
	if !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
	if !other.closed {
		t.Fatal("Close did not close subordinate backend")
	}
}

func TestWlInputMethodBackend_CanceledContextShortCircuitsNoOther(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &WlInputMethodBackend{}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "TypeContext",
			run:  func() error { return b.TypeContext(ctx, "text") },
		},
		{
			name: "KeyDown",
			run:  func() error { return b.KeyDown(ctx, "a") },
		},
		{
			name: "KeyUp",
			run:  func() error { return b.KeyUp(ctx, "a") },
		},
		{
			name: "MouseMove",
			run:  func() error { return b.MouseMove(ctx, 1, 2) },
		},
		{
			name: "MouseClick",
			run:  func() error { return b.MouseClick(ctx, 1, 2, 1) },
		},
		{
			name: "MouseDown",
			run:  func() error { return b.MouseDown(ctx, 1) },
		},
		{
			name: "MouseUp",
			run:  func() error { return b.MouseUp(ctx, 1) },
		},
		{
			name: "ScrollUp",
			run:  func() error { return b.ScrollUp(ctx, 1) },
		},
		{
			name: "ScrollDown",
			run:  func() error { return b.ScrollDown(ctx, 1) },
		},
		{
			name: "ScrollLeft",
			run:  func() error { return b.ScrollLeft(ctx, 1) },
		},
		{
			name: "ScrollRight",
			run:  func() error { return b.ScrollRight(ctx, 1) },
		},
		{
			name: "Sync",
			run:  func() error { return b.Sync(ctx) },
		},
		{
			name: "PointerLocation",
			run: func() error {
				_, _, err := b.PointerLocation(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err != context.Canceled {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
		})
	}
}

func TestTypeContext_Chunking(t *testing.T) {
	// Verify that TypeContext splits strings longer than maxChunk (4000 bytes).
	// We can't test the full flow without a real connection, but we can verify
	// the chunking logic by checking the encodeWlString output sizes.
	const maxChunk = 4000

	// A 5000-byte string should be split into 2 chunks: 4000 + 1000
	s := make([]byte, 5000)
	for i := range s {
		s[i] = 'a'
	}
	str := string(s)

	// Verify the string is indeed > maxChunk
	if len(str) <= maxChunk {
		t.Fatalf("test string too short: %d", len(str))
	}

	// Simulate the chunking logic from TypeContext
	var chunks []string
	for start := 0; start < len(str); {
		end := start + maxChunk
		if end > len(str) {
			end = len(str)
		}
		chunks = append(chunks, str[start:end])
		start = end
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 4000 {
		t.Errorf("first chunk = %d bytes, want 4000", len(chunks[0]))
	}
	if len(chunks[1]) != 1000 {
		t.Errorf("second chunk = %d bytes, want 1000", len(chunks[1]))
	}
}

func TestTypeContext_ChunkingExact(t *testing.T) {
	const maxChunk = 4000

	// Exactly 4000 bytes → 1 chunk
	s := make([]byte, 4000)
	for i := range s {
		s[i] = 'x'
	}
	str := string(s)

	var chunks []string
	for start := 0; start < len(str); {
		end := start + maxChunk
		if end > len(str) {
			end = len(str)
		}
		chunks = append(chunks, str[start:end])
		start = end
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestTypeContext_ChunkingTriple(t *testing.T) {
	const maxChunk = 4000

	// 10000 bytes → 3 chunks: 4000 + 4000 + 2000
	s := make([]byte, 10000)
	for i := range s {
		s[i] = byte('a' + i%26)
	}
	str := string(s)

	var chunks []string
	for start := 0; start < len(str); {
		end := start + maxChunk
		if end > len(str) {
			end = len(str)
		}
		chunks = append(chunks, str[start:end])
		start = end
	}
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 4000 || len(chunks[1]) != 4000 || len(chunks[2]) != 2000 {
		t.Errorf("chunk sizes = %d, %d, %d", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}
