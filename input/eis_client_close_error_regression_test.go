package input

import (
	"errors"
	"net"
	"sync"
	"testing"
)

// An already-retired Unix transport must produce a consistent result for
// every owner that races to close the same EIS client.
func TestEIClientCloseReturnsSameTransportFailureToEveryCaller(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	c := &eiClient{conn: conn, stop: make(chan struct{})}
	const owners = 12
	result := make(chan error, owners)
	var wg sync.WaitGroup
	for range owners {
		wg.Add(1)
		go func() { defer wg.Done(); result <- c.close() }()
	}
	wg.Wait()
	close(result)
	for err := range result {
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("close did not retain the transport failure: %v", err)
		}
	}
	select {
	case <-c.stop:
	default:
		t.Fatal("EIS client shutdown signal was not published")
	}
}
