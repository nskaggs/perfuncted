package input

import (
	"strings"
	"testing"
	"time"
)

// The ei_callback.done event carries exactly one uint64. A peer may not
// complete setup with trailing data that changes the callback frame shape.
func TestEISSetupCallbackRejectsExtraPayloadOnWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"extra word", append(eiU64(0), eiU32(0)...)},
		{"extra uint64", append(eiU64(0), eiU64(0)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			// prepareEISInvalidGrantEndpoint reads the synchronization request,
			// whose first client-created callback is object 1.
			if _, err := server.Write(eiRequest(1, 0, tc.body)); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device setup callback") {
					t.Fatalf("malformed synchronization callback accepted: sender=%v err=%v", result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("extra callback payload left EIS setup awaiting devices")
			}
		})
	}
}
