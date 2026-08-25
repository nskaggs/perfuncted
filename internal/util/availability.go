// Package util provides common utilities used across perfuncted packages.
package util

import (
	"fmt"
	"reflect"
)

// CheckAvailable checks if a resource is available and returns an appropriate error if not.
// It handles typed-nil interface values by using reflection.
func CheckAvailable(name string, resource any) error {
	if IsNil(resource) {
		return fmt.Errorf("%s: not available", name)
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
