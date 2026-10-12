package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

// An unregistered cwd under an archived project must not be refused: the guard
// sees ErrProjectNotFound from ResolveProject and lets the call through.
func TestArchivedParentDoesNotGuardUnregisteredSubdirectory(t *testing.T) {
	ctx := context.Background()
	f := newArchiveGuardFixture(t)
	if _, err := f.s.ArchiveProject(ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}

	cwd := filepath.Join(f.project.RootPath, "unregistered", "sub")
	if _, err := f.s.ResolveProject(ctx, cwd); !errors.Is(err, store.ErrProjectNotFound) {
		t.Fatalf("ResolveProject(%q) err = %v, want ErrProjectNotFound", cwd, err)
	}

	raw, err := json.Marshal(map[string]any{"cwd": cwd, "content": "x", "creator": "human"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.d.dispatchMethod(ctx, rpc.Request{Method: "goal.create", Params: raw})
	if errors.Is(err, store.ErrProjectArchived) {
		t.Fatalf("goal.create under unregistered subdirectory of archived parent refused: %v", err)
	}
}
