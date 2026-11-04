package window

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
)

type cancelOnSwayAdmissionCheck struct {
	context.Context //nolint:containedctx // test cancels at the exact post-gate admission check.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelOnSwayAdmissionCheck) Err() error {
	c.once.Do(c.cancel)
	return c.Context.Err()
}

// A newly available gate cannot authorize an operation whose context expires
// during admission. The returned token must still be restored for the next call.
func TestSwayGateRejectsCancellationAfterAcquiringToken(t *testing.T) {
	m := &SwayManager{}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOnSwayAdmissionCheck{Context: base, cancel: cancel}
	release, err := m.admit(ctx)
	if release != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled gate admission leaked token or succeeded: release=%v err=%v", release != nil, err)
	}
	healthy, err := m.admit(context.Background())
	if err != nil || healthy == nil {
		t.Fatalf("canceled admission permanently consumed gate: %v", err)
	}
	healthy()
}

// Closing while a query waits on the gate must not permit a late transport
// dial, even when the gate is subsequently released.
func TestSwayGateWaiterRefusesClosedManagerWithoutDial(t *testing.T) {
	m := &SwayManager{sock: "unused"}
	hold, err := m.admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m.closed.Store(true)
	hold()
	_, err = m.query(context.Background(), swayMsgGetTree, "")
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed manager admitted a transport query: %v", err)
	}
}
