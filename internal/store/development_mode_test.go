package store

import (
	"context"
	"os"
	"testing"
)

func TestDevelopmentModeIsEnabledPerAgentSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	first := registerNamedTestAgentSession(t, s, "development-mode-first", os.Getpid())
	second := registerNamedTestAgentSession(t, s, "development-mode-second", os.Getpid())
	if err := s.EnableDevelopmentMode(ctx, first); err != nil {
		t.Fatalf("EnableDevelopmentMode: %v", err)
	}
	if enabled, err := s.DevelopmentModeEnabled(ctx, first); err != nil || !enabled {
		t.Fatalf("first development mode = (%v, %v), want (true, nil)", enabled, err)
	}
	if enabled, err := s.DevelopmentModeEnabled(ctx, second); err != nil || enabled {
		t.Fatalf("second development mode = (%v, %v), want (false, nil)", enabled, err)
	}
}
