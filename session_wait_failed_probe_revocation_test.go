package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The backend may report a transient evaluation error while the caller
// withdraws wait authority. Cancellation must win over the retry policy,
// including any failures already accumulated for this wait.
func TestWaitConditionFailureAfterCallerCancellationDoesNotRetry(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("synthetic window discovery read failed")
	probes := 0
	failures := &waitEvaluateFailures{consecutive: 2, total: 4, since: time.Now()}
	condition := Predicate("window-read-failure", func(context.Context) (bool, error) {
		probes++
		cancel()
		return false, failure
	})
	matched, err := session.evaluateWaitCondition(ctx, condition, failures)
	if matched || !errors.Is(err, context.Canceled) || probes != 1 {
		t.Fatalf("revoked condition wait returned matched=%v err=%v probes=%d", matched, err, probes)
	}
	if failures.consecutive != 2 || failures.total != 4 {
		t.Fatalf("canceled evaluation incorrectly retried/recorded backend failure: %+v", failures)
	}
}

// Receipt bookkeeping must be a no-op for a missing optional input bundle,
// preserving the original caller result without manufacturing diagnostics.
func TestMissingInputBundleReceiptIsInertAndDoesNotSwallowError(t *testing.T) {
	var absent *InputBundle
	failure := errors.New("synthetic transport unavailable")
	if err := absent.recordInputReceipt("type", time.Now(), failure); !errors.Is(err, failure) {
		t.Fatalf("missing receipt owner changed failure: %v", err)
	}
	if err := absent.recordInputReceipt("type", time.Now(), nil); err != nil {
		t.Fatalf("missing receipt owner manufactured failure: %v", err)
	}
	if got := absent.recentInputReceipts(); got != nil {
		t.Fatalf("missing input bundle exposed receipts: %v", got)
	}
}
