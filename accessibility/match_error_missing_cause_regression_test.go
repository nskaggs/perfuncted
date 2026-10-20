package accessibility

import (
	"errors"
	"testing"
)

func TestMatchErrorNeverPanicsWhenOptionalCauseMissing(t *testing.T) {
	for _, err := range []*MatchError{
		nil,
		{},
		{Operation: "find button"},
	} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("formatting missing-cause accessibility MatchError panicked: %v", p)
				}
			}()
			var asError error = err
			if str := asError.Error(); str == "" {
				t.Fatal("error formatting returned empty diagnostic")
			}
			if str := err.Error(); str == "" {
				t.Fatal("missing error text")
			}
		}()
	}
}
func TestMatchErrorPreservesCauseIdentity(t *testing.T) {
	cause := errors.New("not found")
	err := &MatchError{Operation: "find button", Err: cause}
	if !errors.Is(err, cause) {
		t.Fatalf("cause lost: %v", err)
	}
}
