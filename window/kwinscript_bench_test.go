//go:build linux

package window

import "testing"

func BenchmarkKWinMoveAction(b *testing.B) {
	for b.Loop() {
		_ = kwinMoveAction(100, 200)
	}
}
