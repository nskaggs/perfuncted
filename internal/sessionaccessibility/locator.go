package sessionaccessibility

import (
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"strings"
)

type LocatorIdentity struct {
	WindowID string
	PID      int32
	AppID    string
	BusName  string
	Name     string
}

func CloneSelector(selector accessibility.Selector) accessibility.Selector {
	selector.States = append([]string(nil), selector.States...)
	selector.Ancestors = append([]accessibility.AncestorSelector(nil), selector.Ancestors...)
	if selector.Attributes != nil {
		attributes := make(map[string]string, len(selector.Attributes))
		for key, value := range selector.Attributes {
			attributes[key] = value
		}
		selector.Attributes = attributes
	}
	return selector
}

//nolint:gocyclo // Scope authority fields must be checked explicitly.
func SameLocatorScope(a, b LocatorIdentity) bool {
	if a.WindowID != "" || b.WindowID != "" {
		if a.WindowID != b.WindowID {
			return false
		}
	}
	if a.BusName != "" && b.BusName != "" && a.BusName != b.BusName {
		return false
	}
	if a.PID != 0 && b.PID != 0 {
		return a.PID == b.PID && sameOptionalIdentity(a.AppID, b.AppID)
	}
	if a.AppID != "" && b.AppID != "" {
		return a.AppID == b.AppID
	}
	if a.BusName != "" && b.BusName != "" {
		return a.BusName == b.BusName
	}
	// A name-only comparison is suitable only when neither observation ever
	// had authoritative identity. Losing PID/bus/app evidence during a retry
	// must not silently downgrade a previously verified semantic target.
	if a.PID != 0 || b.PID != 0 || a.AppID != "" || b.AppID != "" || a.BusName != "" || b.BusName != "" {
		return false
	}
	return a.Name != "" && strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name))
}

func sameOptionalIdentity(a, b string) bool {
	return a == "" || b == "" || a == b
}

func LocatorIdentityChanged(have bool, initial LocatorIdentity, initialRoot accessibility.NodeID, current LocatorIdentity, currentRoot accessibility.NodeID) bool {
	if !have {
		return false
	}
	return !SameLocatorScope(initial, current) || initialRoot.BusName != currentRoot.BusName || initialRoot.ObjectPath != currentRoot.ObjectPath
}

func LocatorResolutionMayRetry(err error) bool {
	return errors.Is(err, accessibility.ErrStaleGeneration) ||
		errors.Is(err, accessibility.ErrStaleNode) ||
		errors.Is(err, accessibility.ErrObservationChanged)
}

// LocatorObservationReady fails closed if the dedicated invalidation stream
// disappeared or already reported a change during semantic resolution.
// Events are only invalidations here, never a source of target identity.
func LocatorObservationReady(stream <-chan accessibility.Event) error {
	if stream == nil {
		return fmt.Errorf("%w: nil semantic observation stream", accessibility.ErrObservationChanged)
	}
	select {
	case _, open := <-stream:
		if !open {
			return fmt.Errorf("%w: semantic observation stream closed", accessibility.ErrObservationChanged)
		}
		return fmt.Errorf("%w: semantic observation invalidated", accessibility.ErrObservationChanged)
	default:
		return nil
	}
}

func LocatorActionMayRetryStale(err error) bool {
	return errors.Is(err, accessibility.ErrStaleGeneration) || errors.Is(err, accessibility.ErrStaleNode)
}

func TextDispatchOutcome(err error) accessibility.DispatchOutcome {
	switch {
	case err == nil:
		return accessibility.DispatchAccepted
	case errors.Is(err, accessibility.ErrStaleGeneration), errors.Is(err, accessibility.ErrStaleNode),
		errors.Is(err, accessibility.ErrUnsupported), errors.Is(err, accessibility.ErrMutationRejected):
		if errors.Is(err, accessibility.ErrMutationRejected) {
			return accessibility.DispatchRejected
		}
		return accessibility.DispatchNotSent
	default:
		return accessibility.DispatchUnknown
	}
}

func UniqueLocatorMatch(nodes []accessibility.Node) (accessibility.Node, error) {
	switch len(nodes) {
	case 0:
		return accessibility.Node{}, accessibility.ErrNotFound
	case 1:
		return nodes[0], nil
	default:
		return accessibility.Node{}, accessibility.ErrAmbiguous
	}
}
