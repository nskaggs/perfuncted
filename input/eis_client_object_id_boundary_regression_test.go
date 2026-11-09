package input

import (
	"sync"
	"testing"
)

// Client-allocated callback IDs must never alias server-owned connection,
// seat, device, or interface objects, even after counter exhaustion.
func TestEIClientObjectIDsStayInsideClientNamespace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start uint64
		want  uint64
	}{
		{"initial client callback", 1, 1},
		{"last client callback", eiServerIDFloor - 1, eiServerIDFloor - 1},
		{"at server boundary", eiServerIDFloor, 0},
		{"past server boundary", eiServerIDFloor + 17, 0},
		{"counter wrapped", 0, 0},
		{"maximum uint64", ^uint64(0), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &eiClient{}
			c.clientID.Store(tc.start)
			got := c.nextClientObjectID()
			if got != tc.want {
				t.Fatalf("counter %x allocated callback %x, want %x", tc.start, got, tc.want)
			}
			if tc.want == eiServerIDFloor-1 {
				if next := c.nextClientObjectID(); next != 0 {
					t.Fatalf("exhausted client namespace allocated object %x", next)
				}
			}
		})
	}
}

func TestEIClientObjectIDAllocationRemainsUniqueAcrossConcurrentCallers(t *testing.T) {
	c := &eiClient{}
	c.clientID.Store(eiServerIDFloor - 8)
	const workers = 16
	ids := make(chan uint64, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() { defer wg.Done(); ids <- c.nextClientObjectID() }()
	}
	wg.Wait()
	close(ids)
	seen := make(map[uint64]bool)
	for id := range ids {
		if id == 0 {
			continue
		}
		if id < eiServerIDFloor-8 || id >= eiServerIDFloor || seen[id] {
			t.Fatalf("invalid or duplicate client object allocation: %x", id)
		}
		seen[id] = true
	}
	if len(seen) != 8 {
		t.Fatalf("allocated %d of 8 remaining distinct client IDs", len(seen))
	}
}
