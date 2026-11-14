//go:build linux

package input

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"golang.org/x/sys/unix"
)

const portalTokenChildEnvironment = "PERFUNCTED_TEST_PORTAL_LOCK_CHILD"

func TestPortalTokenLockExcludedAcrossProcessesAndReacquiredAfterRelease(t *testing.T) {
	if os.Getenv(portalTokenChildEnvironment) == "1" {
		rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + os.Getenv("PERFUNCTED_TEST_PORTAL_LOCK_STATE")})
		lock, err := lockPortalTokenStore(rt)
		if lock != nil {
			defer lock.Close()
		}
		switch os.Getenv("PERFUNCTED_TEST_PORTAL_LOCK_EXPECT") {
		case "busy":
			if lock != nil || !errors.Is(err, unix.EWOULDBLOCK) {
				t.Fatalf("foreign process bypassed token-store lock: lock=%v err=%v", lock, err)
			}
		case "free":
			if err != nil || lock == nil {
				t.Fatalf("foreign process could not acquire released lock: lock=%v err=%v", lock, err)
			}
		default:
			t.Fatal("invalid subprocess test role")
		}
		return
	}
	rt := portalTokenTestRuntime(t)
	first, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	runChild := func(role string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPortalTokenLockExcludedAcrossProcessesAndReacquiredAfterRelease$")
		cmd.Env = append(os.Environ(), portalTokenChildEnvironment+"=1", "PERFUNCTED_TEST_PORTAL_LOCK_STATE="+rt.Get("XDG_STATE_HOME"), "PERFUNCTED_TEST_PORTAL_LOCK_EXPECT="+role)
		output, err := cmd.CombinedOutput()
		if ctx.Err() != nil || err != nil {
			t.Fatalf("child role %s: %v (%v): %s", role, err, ctx.Err(), strings.TrimSpace(string(output)))
		}
	}
	runChild("busy")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	runChild("free")
}
