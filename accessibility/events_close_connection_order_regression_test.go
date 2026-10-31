package accessibility

import (
	"context"
	"testing"
	"time"
)

// Connection close must invalidate every handle epoch, independent of the
// subscriber completion ordering or event dispatcher goroutine scheduling.
func TestSyntheticATSPIClosedConnectionAlwaysInvalidatesHandleGeneration(t *testing.T) {
	for iteration := 0; iteration < 24; iteration++ {
		t.Run("connection-close", func(t *testing.T) {
			_, backend, _ := startSyntheticEventBus(t, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			stream, err := backend.Events(ctx, EventOptions{})
			if err != nil {
				t.Fatal(err)
			}
			connection := backend.eventConnection()
			if connection == nil {
				t.Fatal("no accessibility bus connection")
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case _, open := <-stream:
				if open {
					t.Fatal("closed bus emitted synthetic event")
				}
			case <-time.After(time.Second):
				t.Fatal("stream did not terminate")
			}
			deadline := time.Now().Add(250 * time.Millisecond)
			for backend.Generation() == 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if generation := backend.Generation(); generation != 2 {
				backend.eventsMu.Lock()
				active := backend.eventDone != nil
				subscribers := len(backend.subscribers)
				retired := backend.eventsRetired
				backend.eventsMu.Unlock()
				backend.mu.RLock()
				disconnected := backend.disconnected
				backend.mu.RUnlock()
				t.Fatalf("closed connection retained generation=%d, connected=%v ctx=%v active=%v subscribers=%d retired=%v disconnected=%v", generation, connection.Connected(), connection.Context().Err(), active, subscribers, retired, disconnected)
			}
		})
	}
}
