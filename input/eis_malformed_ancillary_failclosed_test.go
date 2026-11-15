package input

import (
	"encoding/binary"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A peer's malformed ancillary framing cannot authorize a file descriptor
// release based on an incomplete or forged control-message header.
func TestEISMalformedAncillaryHeaderIsDiscarded(t *testing.T) {
	header := make([]byte, unix.CmsgSpace(4))
	// Claim a payload larger than the actual received control buffer.
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.NativeEndian.PutUint64(header[:8], uint64(len(header)+256))
	} else {
		binary.NativeEndian.PutUint32(header[:4], uint32(len(header)+256))
	}
	closeReceivedFDs(header)
}

// A syntactically valid SCM_RIGHTS header with a partial descriptor payload
// must not be decoded as an integer descriptor or closed.
func TestEISPartialRightsPayloadIsIgnored(t *testing.T) {
	control := unix.UnixRights(0)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.NativeEndian.PutUint64(control[:8], uint64(unix.CmsgLen(1)))
	} else {
		binary.NativeEndian.PutUint32(control[:4], uint32(unix.CmsgLen(1)))
	}
	closeReceivedFDs(control[:unix.CmsgLen(1)])
}
