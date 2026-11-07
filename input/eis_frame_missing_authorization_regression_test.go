package input

import (
	"errors"
	"testing"
)

// Frame constructors must not encode events for unbound capabilities or
// panic when a revoked/absent sender is passed into a direct helper.
func TestEISFrameConstructorsRejectUnboundCapabilitiesWithoutPanicking(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"nil pointer sender", func() error { _, err := eiPointerFrame(nil, 10, 10); return err }},
		{"nil text sender", func() error { _, err := eiTextFrames(nil, "private text"); return err }},
		{"nil scroll sender", func() error { _, err := eiScrollFrame(nil, 0, 120); return err }},
		{"scroll no client", func() error { _, err := eiScrollFrame(&eiSender{scroll: 11, scrollDev: 12}, 0, 120); return err }},
		{"scroll no interface", func() error {
			_, err := eiScrollFrame(&eiSender{client: &eiClient{}, scrollDev: 12}, 0, 120)
			return err
		}},
		{"scroll no device", func() error { _, err := eiScrollFrame(&eiSender{client: &eiClient{}, scroll: 11}, 0, 120); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("unbound frame constructor panicked: %v", p)
				}
			}()
			if err := tc.run(); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unbound frame returned %v rather than unsupported", err)
			}
		})
	}
}
