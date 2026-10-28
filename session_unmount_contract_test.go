package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseSessionSubdirMountsDecodesKernelEscapes(t *testing.T) {
	root := "/tmp/perfuncted-xdg-abc with space"
	info := []byte("24 23 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space rw - tmpfs tmpfs rw\n" +
		"25 24 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space/inner\\040dir rw - tmpfs tmpfs rw\n" +
		"26 23 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space-other rw - tmpfs tmpfs rw\n")
	got := parseSessionSubdirMounts(root, info)
	want := []string{root, root + "/inner dir"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escaped mountpoints: got %q, want %q", got, want)
	}
}

func TestUnmountSubdirsWithPreservesUnmountedState(t *testing.T) {
	const mount = "/tmp/perfuncted-xdg-test/nested"
	tests := []struct {
		name       string
		after      []string
		unmountErr error
		wantErr    bool
	}{
		{"mount still present", []string{mount}, errors.New("fusermount failed"), true},
		{"mount actually gone despite helper error", nil, errors.New("mount raced away"), false},
		{"normal successful unmount", nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reads, calls := 0, 0
			list := func(string) ([]string, error) {
				reads++
				if reads == 1 {
					return []string{mount}, nil
				}
				return tc.after, nil
			}
			err := unmountSubdirsWith(context.Background(), "/tmp/perfuncted-xdg-test", list,
				func(_ context.Context, path string) error {
					calls++
					if path != mount {
						t.Fatalf("wrong unmount target %q", path)
					}
					return tc.unmountErr
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "mount") {
				t.Fatalf("unclassified error: %v", err)
			}
			if calls != 1 || reads != 2 {
				t.Fatalf("calls=%d reads=%d", calls, reads)
			}
		})
	}
}

func TestUnmountSubdirsWithHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := unmountSubdirsWith(ctx, "/tmp/perfuncted-xdg-test",
		func(string) ([]string, error) { return []string{"/tmp/perfuncted-xdg-test/a"}, nil },
		func(_ context.Context, _ string) error { calls++; cancel(); return context.Canceled })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestUnmountSubdirsWithDeepestFirst(t *testing.T) {
	const root = "/tmp/perfuncted-xdg-nested"
	original := []string{root + "/child/grandchild", root, root + "/child"}
	var order []string
	rounds := 0
	err := unmountSubdirsWith(context.Background(), root,
		func(string) ([]string, error) {
			rounds++
			if rounds == 1 {
				return original, nil
			}
			return nil, nil
		},
		func(_ context.Context, m string) error { order = append(order, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root + "/child/grandchild", root + "/child", root}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unsafe unmount order: %v, want %v", order, want)
	}
}

func TestUnmountSubdirsWithFailsClosedWhenMountsCannotBeRead(t *testing.T) {
	want := errors.New("mountinfo unavailable")
	invoked := false
	err := unmountSubdirsWith(context.Background(), "/tmp/perfuncted-xdg-test",
		func(string) ([]string, error) { return nil, want },
		func(_ context.Context, _ string) error { invoked = true; return nil })
	if !errors.Is(err, want) || invoked {
		t.Fatalf("err=%v invoked=%v", err, invoked)
	}
}
