package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestLiveProviderAcceptanceRequiresExplicitSideEffectGate(t *testing.T) {
	t.Setenv("DENYRA_LIVE_PROVIDER_ACCEPTANCE", "")
	err := run(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), []string{"live-provider-acceptance"})
	if err == nil || !strings.Contains(err.Error(), "explicit side-effect gate") {
		t.Fatalf("live provider gate error = %v", err)
	}
}
