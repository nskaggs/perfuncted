package main

import (
	"context"
	"github.com/nskaggs/perfuncted"
	"testing"
)

func TestRemoteDesktopCLIRequiresExplicitOptIn(t *testing.T) {
	root := newRootCmd(func(cfg *cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	if root.PersistentFlags().Lookup("input-backend") == nil {
		t.Fatal("missing input-backend option to select compositor-authorized portal")
	}
	if root.PersistentFlags().Lookup("remember-input-permission") == nil {
		t.Fatal("missing explicit persistence setting")
	}
}
