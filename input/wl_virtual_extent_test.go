package input

import (
	"sync"
	"testing"
)

// The logical extent is derived from the reported physical size and scale, and
// a scale event that arrives before any mode event must not divide by zero.
func TestOutputGeometryDerivesLogicalExtent(t *testing.T) {
	for _, tc := range []struct {
		name       string
		apply      func(*WlVirtualBackend)
		wantWidth  uint32
		wantHeight uint32
	}{
		{
			name:       "fallback until a mode event arrives",
			apply:      func(*WlVirtualBackend) {},
			wantWidth:  1920,
			wantHeight: 1080,
		},
		{
			name:       "mode event at scale one",
			apply:      func(b *WlVirtualBackend) { b.applyOutputMode(3840, 2160) },
			wantWidth:  3840,
			wantHeight: 2160,
		},
		{
			name: "scaled logical extent",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputMode(3840, 2160)
				b.applyOutputScale(2)
			},
			wantWidth:  1920,
			wantHeight: 1080,
		},
		{
			name: "rescaling recomputes from the physical size",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputMode(3840, 2160)
				b.applyOutputScale(2)
				b.applyOutputScale(4)
			},
			wantWidth:  960,
			wantHeight: 540,
		},
		{
			name:       "zero scale is treated as one",
			apply:      func(b *WlVirtualBackend) { b.applyOutputScale(0) },
			wantWidth:  1920,
			wantHeight: 1080,
		},
		{
			name: "scale before any mode keeps the known extent",
			apply: func(b *WlVirtualBackend) {
				b.applyOutputScale(2)
				b.applyOutputMode(3840, 2160)
			},
			wantWidth:  1920,
			wantHeight: 1080,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlVirtualBackend{}
			b.setOutputExtentFallback(1920, 1080)
			tc.apply(b)

			width, height := b.outputExtent()
			if width != tc.wantWidth || height != tc.wantHeight {
				t.Fatalf("extent = %dx%d, want %dx%d", width, height, tc.wantWidth, tc.wantHeight)
			}
		})
	}
}

// wl_output events arrive on the shared session's dispatch goroutine while
// input operations read the extent to validate and encode motion_absolute
// events, so both sides must be synchronized and a reader must never observe a
// half-applied geometry change.
//
// Every goroutine is parked on a start barrier and released together, so the
// readers and the writer provably overlap instead of depending on scheduling.
func TestOutputExtentIsSafeDuringConcurrentGeometryChanges(t *testing.T) {
	const (
		readers    = 4
		iterations = 20000
	)

	b := &WlVirtualBackend{}
	b.setOutputExtentFallback(1920, 1080)

	var ready, done sync.WaitGroup
	start := make(chan struct{})

	for range readers {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			for range iterations {
				width, height := b.outputExtent()
				// Every extent this backend derives comes from a 3840x2160 mode
				// at scale 1 to 4, so a reader must never see a torn pair: a
				// zero edge, or a width and height from different updates.
				if width == 0 || height == 0 {
					t.Errorf("observed a collapsed extent %dx%d", width, height)
					return
				}
				if width*9 != height*16 {
					t.Errorf("observed a torn extent %dx%d for a 16:9 mode", width, height)
					return
				}
			}
		}()
	}

	ready.Add(1)
	done.Add(1)
	go func() {
		defer done.Done()
		ready.Done()
		<-start
		for i := range iterations {
			b.applyOutputMode(3840, 2160)
			b.applyOutputScale(uint32(i%4) + 1)
		}
	}()

	ready.Wait()
	close(start)
	done.Wait()
}
