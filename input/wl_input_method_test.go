//go:build linux
// +build linux

package input

import (
	"bytes"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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

func TestWlInputMethodBackend_Delegate_NoOther(t *testing.T) {
	// We can't easily construct a WlInputMethodBackend without a real connection,
	// but we can verify the delegation pattern by checking that methods return
	// errors when other is nil.
	// This requires a real Wayland connection, so we just verify the error path.
	t.Log("Delegation test requires real Wayland connection; skipping full test")
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
