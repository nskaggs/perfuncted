package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted"
)

func ExampleCapabilityStatus_Supports() {
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	status := session.Capability(perfuncted.CapabilityScreen)
	fmt.Println(status.Available, status.Supports("capture"))
	// Output: false false
}

func ExampleCapabilityError() {
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	_, err := session.Screen.Grab(context.Background(), imageRect())
	fmt.Println(errors.Is(err, perfuncted.ErrUnavailable))
	// Output: true
}

func ExampleSession_Wait_cancellation() {
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := session.Wait(ctx, perfuncted.Predicate("never", func(context.Context) (bool, error) {
		return false, nil
	}))
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}

// imageRect keeps the examples focused on the public session contract.
func imageRect() (r image.Rectangle) {
	return image.Rect(0, 0, 1, 1)
}
