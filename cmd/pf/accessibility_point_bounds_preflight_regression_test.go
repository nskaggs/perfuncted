package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
)

func TestAccessibilityAtPointRejectsOutOfRangeCoordinatesBeforeOpen(t *testing.T) {
	cases := [][]string{
		{"a11y", "at-point", "2147483648", "0"},
		{"a11y", "at-point", "--", "0", "-2147483649"},
		{"a11y", "at-point", "--x=2147483648"},
		{"a11y", "at-point", "--y=-2147483649"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("opened accessibility session for out-of-range point: %d", opened)
			}
			if code == 0 || !strings.Contains(errText, "range") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}

func TestAccessibilityAtPointAcceptsSigned32BitBoundaryCoordinates(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "at-point", "--x=2147483647", "--y=-2147483648"},
		{"a11y", "at-point", "--", "-2147483648", "2147483647"},
		{"a11y", "at-point"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, _, _ = captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("synthetic session opener reached")
				}
			})
			if opened != 1 {
				t.Fatalf("valid 32-bit coordinate should reach session opener once, got %d", opened)
			}
		})
	}
}
