package screen

import "fmt"

// captureBufferSize validates the 4-byte-per-pixel shared-memory geometry used
// by the capture backends and returns the allocation size accepted by wl_shm.
func captureBufferSize(width, height, stride uint32) (int, error) {
	if width == 0 || height == 0 {
		return 0, fmt.Errorf("capture geometry has zero dimensions")
	}
	rowBytes := uint64(width) * 4
	if uint64(stride) < rowBytes {
		return 0, fmt.Errorf("capture stride %d is smaller than row width %d", stride, rowBytes)
	}
	size := uint64(stride) * uint64(height)
	maxInt := uint64(^uint(0) >> 1)
	if size > maxInt || size > uint64(1<<31-1) {
		return 0, fmt.Errorf("capture buffer size %d is too large", size)
	}
	return int(size), nil
}
