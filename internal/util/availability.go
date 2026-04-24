// Package util provides common utilities used across perfuncted packages.
package util

import "fmt"

// CheckAvailable checks if a resource is available and returns an appropriate error if not.
func CheckAvailable(name string, resource interface{}) error {
	if resource == nil {
		return fmt.Errorf("%s: not available", name)
	}
	return nil
}