package accessibility

import (
	"reflect"
	"testing"
)

// A caller-supplied response budget must not override the absolute snapshot
// ceiling, and canonical role filters must not mutate caller-owned inputs.
func TestSnapshotOptionsClampTotalResponseAndCopyRoleFilters(t *testing.T) {
	roles := []string{"  ALERT ", "button", "Alert", "", "BUTTON"}
	opts := SnapshotOptions{
		MaxDepth:      absMaxDepth + 1,
		MaxNodes:      absMaxNodes + 1,
		MaxTextBytes:  absMaxText + 1,
		MaxTotalBytes: absMaxTotal + 1,
		SkipRoles:     roles,
	}
	normalized := opts.normalized()
	if normalized.MaxDepth != absMaxDepth || normalized.MaxNodes != absMaxNodes ||
		normalized.MaxTextBytes != absMaxText || normalized.MaxTotalBytes != absMaxTotal {
		t.Fatalf("oversized snapshot limits escaped caps: %+v", normalized)
	}
	if want := []string{"alert", "button"}; !reflect.DeepEqual(normalized.SkipRoles, want) {
		t.Fatalf("role filter normalization = %#v, want %#v", normalized.SkipRoles, want)
	}
	if want := []string{"  ALERT ", "button", "Alert", "", "BUTTON"}; !reflect.DeepEqual(roles, want) {
		t.Fatalf("normalizing snapshot options mutated caller-owned filters: %#v", roles)
	}
	if got := (SnapshotOptions{MaxTotalBytes: -1}).normalized().MaxTotalBytes; got != defaultMaxTotal {
		t.Fatalf("negative total budget default = %d, want %d", got, defaultMaxTotal)
	}
}
