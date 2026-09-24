// Package util provides common utilities used across perfuncted packages.
package util

import (
	"errors"
	"fmt"
	"reflect"
)

// ErrNotAvailable reports that a required resource handle is nil.
var ErrNotAvailable = errors.New("not available")

// CheckAvailable checks if a resource is available and returns an appropriate error if not.
// It handles typed-nil interface values by using reflection.
func CheckAvailable(name string, resource any) error {
	if IsNil(resource) {
		return fmt.Errorf("%s: %w", name, ErrNotAvailable)
	}
	return nil
}

// IsNil reports whether resource is nil, including typed-nil interfaces.
func IsNil(resource any) bool {
	if resource == nil {
		return true
	}
	v := reflect.ValueOf(resource)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice: //nolint:govet // reflect.Ptr is already inlined
		return v.IsNil()
	default:
		return false
	}
}
