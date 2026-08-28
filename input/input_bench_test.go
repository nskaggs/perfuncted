//go:build linux
// +build linux

package input

import (
	"testing"
)

func BenchmarkParseKeySend_Literal(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("hello world this is a test string")
	}
}

func BenchmarkParseKeySend_SingleKey(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{enter}")
	}
}

func BenchmarkParseKeySend_Combo(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{ctrl+shift+t}")
	}
}

func BenchmarkParseKeySend_Mixed(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("Hello{enter}{ctrl+s}World{escape}")
	}
}

func BenchmarkParseKeySend_HoldRelease(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{ctrl down}v{ctrl up}")
	}
}
