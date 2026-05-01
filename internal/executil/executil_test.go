package executil_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/nskaggs/perfuncted/internal/executil"
)

func sortedCopy(ss []string) []string {
	out := slices.Clone(ss)
	slices.Sort(out)
	return out
}

func TestMergeEnvVarious(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		base  []string
		extra []string
		want  []string
	}{
		{
			name: "preserve base",
			base: []string{"A=1", "B=2"},
			want: []string{"A=1", "B=2"},
		},
		{
			name:  "overlay overrides and adds",
			base:  []string{"PATH=/usr/bin", "XDG=old"},
			extra: []string{"XDG=new", "DBUS=unix:path=/tmp/session/bus"},
			want:  []string{"PATH=/usr/bin", "XDG=new", "DBUS=unix:path=/tmp/session/bus"},
		},
		{
			name:  "empty value in extra unsets",
			base:  []string{"XDG=old"},
			extra: []string{"XDG="},
			want:  []string{"XDG="},
		},
		{
			name:  "base entry without equals treated as empty value",
			base:  []string{"FOO", "BAR=2"},
			extra: []string{"BAR=3"},
			want:  []string{"FOO=", "BAR=3"},
		},
		{
			name: "duplicate keys in base: last wins",
			base: []string{"A=1", "A=2", "B=3"},
			want: []string{"A=2", "B=3"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := executil.MergeEnv(tc.extra, tc.base)
			want := sortedCopy(tc.want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("MergeEnv(%v, %v) = %v; want %v", tc.extra, tc.base, got, want)
			}

			// Determinism: calling again must yield same output
			got2 := executil.MergeEnv(tc.extra, tc.base)
			if !reflect.DeepEqual(got2, got) {
				t.Fatalf("MergeEnv is non-deterministic: first=%v second=%v", got, got2)
			}
		})
	}
}
