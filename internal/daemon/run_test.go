package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestDaemonRegistersRunAndAssociatesItWithGoalProject(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	d := New(s)

	registerParams, err := json.Marshal(map[string]string{"run_id": "run-startup"})
	if err != nil {
		t.Fatalf("marshal run.register params: %v", err)
	}
	result, err := d.dispatch(ctx, rpc.Request{Method: "run.register", Params: registerParams})
	if err != nil {
		t.Fatalf("run.register: %v", err)
	}
	var registered map[string]bool
	if err := json.Unmarshal(result, &registered); err != nil {
		t.Fatalf("unmarshal run.register result: %v", err)
	}
	if !registered["ok"] {
		t.Fatalf("run.register result = %s, want ok=true", result)
	}

	var projectID sql.NullString
	if err := s.DB().QueryRowContext(ctx, `SELECT project_id FROM runs WHERE id = ?`, "run-startup").Scan(&projectID); err != nil {
		t.Fatalf("lookup registered run: %v", err)
	}
	if projectID.Valid {
		t.Fatalf("registered run project_id = %q, want unbound before goal.list", projectID.String)
	}

	goalListParams, err := json.Marshal(map[string]string{
		"cwd":    "/repos/atct/subdir",
		"run_id": "run-startup",
	})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "goal.list", Params: goalListParams}); err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT project_id FROM runs WHERE id = ?`, "run-startup").Scan(&projectID); err != nil {
		t.Fatalf("lookup associated run: %v", err)
	}
	if !projectID.Valid || projectID.String != project.ID {
		t.Fatalf("associated run project_id = %q (valid=%t), want %q", projectID.String, projectID.Valid, project.ID)
	}
}
