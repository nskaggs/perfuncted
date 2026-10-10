package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
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

func ExampleAccessibilityLocator_ClickAndWait() {
	verifiedSave := func(ctx context.Context, session *perfuncted.Session, window *perfuncted.Window, saved perfuncted.Condition) error {
		locator := session.Accessibility.LocatorForWindow(window.ID().String(), accessibility.Selector{
			Role: "button", Name: "Save",
		}, accessibility.SnapshotOptions{})
		receipt, _, err := locator.ClickAndWait(ctx, saved)
		if err != nil {
			return err
		}
		if receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != perfuncted.ActionOutcomeVerified {
			return errors.New("save action lacks accepted dispatch or independent verification")
		}
		return nil
	}
	_ = verifiedSave
	// Output:
}

// imageRect keeps the examples focused on the public session contract.
func imageRect() (r image.Rectangle) {
	return image.Rect(0, 0, 1, 1)
}
