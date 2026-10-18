package perfuncted

import (
	"context"
	"testing"
	"time"
)

func benchmarkInputReceiptBundle() *InputBundle {
	s := &Session{
		ctx: context.Background(),
		capabilities: map[Capability]CapabilityStatus{
			CapabilityInput: {
				Capability:  CapabilityInput,
				Available:   true,
				Backend:     "*input.WlVirtualBackend",
				Operations:  []string{"keyboard", "click", "pointer", "scroll", "sync", "pointer-location", "pointer-coordinate-space", "drag"},
				Diagnostics: []string{"wayland", "screen", "keyboard", "pointer"},
			},
		},
	}
	return &InputBundle{bundleBase: bundleBase{session: s, capability: CapabilityInput}}
}

func BenchmarkSyntheticInputReceiptRecordingWithSession(b *testing.B) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit; i++ {
		_ = input.recordInputReceipt("type", start, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := input.recordInputReceipt("type", start, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSyntheticInputReceiptDiagnosticSnapshot(b *testing.B) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit; i++ {
		_ = input.recordInputReceipt("type", start, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		receipts := input.recentInputReceipts()
		if len(receipts) != inputReceiptHistoryLimit || receipts[0].Mechanism != "*input.WlVirtualBackend" {
			b.Fatal("receipt snapshot lost backend identity")
		}
	}
}

// A diagnostic snapshot must preserve order, backend identity and isolation,
// even though normal input actions should not have to resolve that identity.
func TestInputReceiptMechanismOnDiagnosticSnapshot(t *testing.T) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit+7; i++ {
		_ = input.recordInputReceipt("type", start, nil)
	}
	receipts := input.recentInputReceipts()
	if len(receipts) != inputReceiptHistoryLimit {
		t.Fatalf("count=%d", len(receipts))
	}
	for i, receipt := range receipts {
		wantSeq := uint64(i + 8)
		if receipt.Sequence != wantSeq || receipt.Mechanism != "*input.WlVirtualBackend" {
			t.Fatalf("receipt[%d]=%+v want seq=%d and backend", i, receipt, wantSeq)
		}
	}
	receipts[0].Mechanism = "mutated"
	if input.recentInputReceipts()[0].Mechanism == "mutated" {
		t.Fatal("diagnostic receipt snapshot aliases retained history")
	}
}
