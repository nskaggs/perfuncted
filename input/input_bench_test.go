//go:build linux
// +build linux

package input

import (
	"testing"
)

func BenchmarkParseKeySend_Literal(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseKeySend("hello world this is a test string")
	}
}

func BenchmarkParseKeySend_SingleKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseKeySend("{enter}")
	}
}

func BenchmarkParseKeySend_Combo(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseKeySend("{ctrl+shift+t}")
	}
}

func BenchmarkParseKeySend_Mixed(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseKeySend("Hello{enter}{ctrl+s}World{escape}")
	}
}

func BenchmarkParseKeySend_HoldRelease(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseKeySend("{ctrl down}v{ctrl up}")
	}
}
