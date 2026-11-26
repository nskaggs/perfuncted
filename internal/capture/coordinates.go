package capture

import "math/bits"

func AxisSpan(min, max int) (uint64, bool) {
	if max <= min {
		return 0, false
	}
	span := uint64(max) - uint64(min)
	return span, span <= uint64(^uint(0)>>1)
}

func ScaleOffset(offset, sourceSpan, destinationSpan uint64) uint64 {
	high, low := bits.Mul64(offset, destinationSpan)
	quotient, _ := bits.Div64(high, low, sourceSpan)
	return quotient
}
