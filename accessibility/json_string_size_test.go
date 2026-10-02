package accessibility

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// referenceJSONStringSize is the pre-optimization implementation, kept here so
// the table-driven version can be proven to produce identical sizes.
func referenceJSONStringSize(value string) int {
	n := 2
	for i := 0; i < len(value); {
		switch value[i] {
		case '"', '\\':
			n = addJSONSize(n, 2)
			i++
		case '\b', '\f', '\n', '\r', '\t':
			n = addJSONSize(n, 2)
			i++
		case '<', '>', '&':
			n = addJSONSize(n, 6)
			i++
		default:
			if value[i] < 0x20 {
				n = addJSONSize(n, 6)
				i++
				continue
			}
			if value[i] < utf8.RuneSelf {
				n = addJSONSize(n, 1)
				i++
				continue
			}
			r, size := utf8.DecodeRuneInString(value[i:])
			if r == utf8.RuneError && size == 1 {
				n = addJSONSize(n, 3)
				i++
				continue
			}
			if r == '\u2028' || r == '\u2029' {
				n = addJSONSize(n, 6)
			} else {
				n = addJSONSize(n, size)
			}
			i += size
		}
	}
	return n
}

func TestJSONStringSizeMatchesReferenceForEveryByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		value := string([]byte{byte(b)})
		got, want := jsonStringSize(value), referenceJSONStringSize(value)
		if got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestJSONStringSizeMatchesReferenceOnMixedInput(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for range 200000 {
		n := rng.Intn(24)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = all[rng.Intn(len(all))]
		}
		value := string(buf)
		if got, want := jsonStringSize(value), referenceJSONStringSize(value); got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
	// Valid multi-byte runes, including the ones that need escaping.
	for r := rune(0x80); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		value := fmt.Sprintf("a%cb", r)
		if got, want := jsonStringSize(value), referenceJSONStringSize(value); got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
	// Long realistic strings.
	for _, s := range []string{
		strings.Repeat("plain ascii text ", 50),
		strings.Repeat(`quote " backslash \ angle < gt > amp & `, 20),
		strings.Repeat("Ünïcödé", 40),
		strings.Repeat("\x00\x01\x1f\x7f\xff", 30),
		strings.Repeat("日本語テキスト", 40),
	} {
		if got, want := jsonStringSize(s), referenceJSONStringSize(s); got != want {
			t.Fatalf("jsonStringSize(long %d bytes) = %d, want %d", len(s), got, want)
		}
	}
}
