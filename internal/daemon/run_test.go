package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestDaemonRegistersAgentSessionAndAssociatesItWithGoalProject(t *testing.T) {
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

	registerParams, err := json.Marshal(map[string]any{"pid": os.Getpid()})
	if err != nil {
		t.Fatalf("marshal run.register params: %v", err)
	}
	result, err := d.dispatch(ctx, rpc.Request{Method: "run.register", Params: registerParams})
	if err != nil {
		t.Fatalf("run.register: %v", err)
	}
	var registered struct {
		OK             bool  `json:"ok"`
		AgentSessionID int64 `json:"agent_session_id"`
	}
	if err := json.Unmarshal(result, &registered); err != nil {
		t.Fatalf("unmarshal run.register result: %v", err)
	}
	if !registered.OK || registered.AgentSessionID == 0 {
		t.Fatalf("run.register result = %v, want ok=true", result)
	}
	agentSessionID := registered.AgentSessionID

	var projectID sql.NullInt64
	if err := s.DB().QueryRowContext(ctx, `SELECT project_id FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&projectID); err != nil {
		t.Fatalf("lookup registered run: %v", err)
	}
	if projectID.Valid {
		t.Fatalf("registered run project_id = %v, want unbound before goal.list", projectID.Int64)
	}
	var registeredPID int
	var startedAt string
	if err := s.DB().QueryRowContext(ctx, `
		SELECT pid, started_at FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&registeredPID, &startedAt); err != nil {
		t.Fatalf("lookup registered process identity: %v", err)
	}
	if registeredPID != os.Getpid() || startedAt == "" {
		t.Fatalf("registered process identity = pid %d, started_at %v; want pid %d and a non-empty start time", registeredPID, startedAt, os.Getpid())
	}

	goalListParams, err := json.Marshal(map[string]any{
		"cwd":              "/repos/atct/subdir",
		"agent_session_id": agentSessionID,
	})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "goal.list", Params: goalListParams}); err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	if err := s.DB().QueryRowContext(ctx, `SELECT project_id FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&projectID); err != nil {
		t.Fatalf("lookup associated run: %v", err)
	}
	if !projectID.Valid || projectID.Int64 != project.ID {
		t.Fatalf("associated run project_id = %v (valid=%t), want %v", projectID.Int64, projectID.Valid, project.ID)
	}
}

func TestRunRegisterAllocatesSequentialAgentSessionID(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	d := New(s)
	params, err := json.Marshal(map[string]any{"pid": 0})
	if err != nil {
		t.Fatalf("marshal run.register params: %v", err)
	}
	result, err := d.dispatch(context.Background(), rpc.Request{Method: "run.register", Params: params})
	if err != nil {
		t.Fatalf("run.register: %v", err)
	}
	var response struct {
		AgentSessionID int64 `json:"agent_session_id"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal run.register result: %v", err)
	}
	if response.AgentSessionID != 1 {
		t.Fatalf("run.register agent_session_id = %d, want 1", response.AgentSessionID)
	}

	result, err = d.dispatch(context.Background(), rpc.Request{Method: "run.register", Params: params})
	if err != nil {
		t.Fatalf("second run.register: %v", err)
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal second run.register result: %v", err)
	}
	if response.AgentSessionID != 2 {
		t.Fatalf("second run.register agent_session_id = %d, want 2", response.AgentSessionID)
	}
}
