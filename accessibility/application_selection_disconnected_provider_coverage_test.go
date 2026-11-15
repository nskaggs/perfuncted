package accessibility

import (
	"context"
	"errors"
	"testing"
)

// An AT-SPI application selector is not allowed to publish an identity or
// treat a disappeared provider as a no-match result. Cached enumeration,
// fresh enumeration, and toolkit lookup all fail closed without a live bus.
func TestApplicationSelectionRejectsDisconnectedProviderWithoutIdentity(t *testing.T) {
	const generation = 31
	b := &dbusBackend{generation: generation}
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: generation}
	filter := ApplicationFilter{Name: "Editor", Bus: id.BusName}
	for _, tc := range []struct {
		name string
		run  func() (Application, error)
	}{
		{"cached", func() (Application, error) { return b.FindApplication(context.Background(), filter) }},
		{"fresh", func() (Application, error) { return b.FindApplicationFresh(context.Background(), filter) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := tc.run()
			if !errors.Is(err, ErrDisconnected) || app.Node.ID != (NodeID{}) || app.Name != "" {
				t.Fatalf("disconnected identity disclosed or misreported: %+v %v", app, err)
			}
		})
	}
	if toolkit, err := b.toolkitForNode(context.Background(), id); !errors.Is(err, ErrDisconnected) || toolkit != "" {
		t.Fatalf("disconnected provider toolkit trusted: %q %v", toolkit, err)
	}
}
