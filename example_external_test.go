package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/window"
)

func ExampleOpen() {
	s, err := perfuncted.Open(perfuncted.Require(perfuncted.CapabilityInput))
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.Do(perfuncted.Type("Invoice {A} + tax"), perfuncted.Key("tab"), perfuncted.Type("12.34"))
}

// ExampleWindowTarget_Do compiles the opt-in window-targeted scripting path.
// A real session requires a managed desktop with observable window identity.
func ExampleWindowTarget_Do() {
	s, err := perfuncted.Open(perfuncted.Require(perfuncted.CapabilityInput), perfuncted.Require(perfuncted.CapabilityWindows))
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.Window("Editor").Do(perfuncted.Type("Hello"), perfuncted.Key("tab"))
}

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

	_, err := session.Screen().WithContext(context.Background()).Grab(imageRect())
	fmt.Println(errors.Is(err, perfuncted.ErrUnavailable))
	// Output: true
}

func ExampleSession_Wait_cancellation() {
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := session.WithContext(ctx).Wait(perfuncted.Predicate("never", func(context.Context) (bool, error) {
		return false, nil
	}))
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}

func ExampleAccessibilityLocator_ClickAndWait() {
	verifiedSave := func(ctx context.Context, session *perfuncted.Session, window *perfuncted.Window, saved perfuncted.Condition) error {
		locator := session.Accessibility().LocatorForWindow(window.ID().String(), accessibility.Selector{
			Role: "button", Name: "Save",
		}, accessibility.SnapshotOptions{})
		receipt, _, err := locator.WithContext(ctx).ClickAndWait(saved)
		if err != nil {
			return err
		}
		if receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != accessibility.ActionOutcomeVerified {
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

// ExampleWindow_Do shows reuse of one discovered window without a title lookup.
func ExampleWindow_Do() {
	s, err := perfuncted.Open()
	if err != nil {
		return
	}
	defer s.Close()
	w, err := s.Windows().Find(window.Title("Editor"))
	if err != nil {
		return
	}
	if err := w.Do(perfuncted.Type("Hello"), perfuncted.Key("enter")); err != nil {
		return
	}
	_, _ = w.Capture()
}

func ExampleWindow_Locator() {
	s, err := perfuncted.Open(perfuncted.Require(perfuncted.CapabilityWindows, perfuncted.CapabilityAccessibility))
	if err != nil {
		return
	}
	defer s.Close()
	w, err := s.Windows().Find(window.Title("Editor"))
	if err != nil {
		return
	}
	_, _ = w.Locator(accessibility.Selector{Role: "button", Name: "Save"}).Click()
}
