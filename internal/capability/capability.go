package capability

import "fmt"

// UnsupportedError describes an operation that the selected backend cannot perform.
type UnsupportedError struct {
	Surface string
	Backend string
	Reason  string
}

func (e UnsupportedError) Error() string {
	switch {
	case e.Surface != "" && e.Backend != "" && e.Reason != "":
		return fmt.Sprintf("%s: unsupported on %s: %s", e.Surface, e.Backend, e.Reason)
	case e.Surface != "" && e.Reason != "":
		return fmt.Sprintf("%s: unsupported: %s", e.Surface, e.Reason)
	case e.Surface != "":
		return fmt.Sprintf("%s: unsupported", e.Surface)
	default:
		return "unsupported"
	}
}

// Unsupported returns a typed error for a missing capability.
func Unsupported(surface, backend, reason string) error {
	return UnsupportedError{Surface: surface, Backend: backend, Reason: reason}
}
