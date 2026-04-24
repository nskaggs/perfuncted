// Package util provides common utilities used across perfuncted packages.
package util

import (
	"fmt"
	"reflect"
)

// CheckAvailable checks if a resource is available and returns an appropriate error if not.
// It handles typed-nil interface values by using reflection.
func CheckAvailable(name string, resource interface{}) error {
	if resource == nil {
		return fmt.Errorf("%s: not available", name)
	}
	v := reflect.ValueOf(resource)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if v.IsNil() {
			return fmt.Errorf("%s: not available", name)
		}
	}
	return nil
}
