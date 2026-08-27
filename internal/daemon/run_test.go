package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	agentSessionID := runRegisterForTest(t, d, os.Getpid(), project.RootPath)
	projectID := registeredAgentSessionProjectID(t, s, agentSessionID)
	if !projectID.Valid || projectID.Int64 != project.ID {
		t.Fatalf("registered run project_id = %v (valid=%t), want %v", projectID.Int64, projectID.Valid, project.ID)
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
}

func TestRunRegisterResolvesProjectFromSubdirectory(t *testing.T) {
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
	agentSessionID := runRegisterForTest(t, New(s), 0, "/repos/atct/subdir")
	projectID := registeredAgentSessionProjectID(t, s, agentSessionID)
	if !projectID.Valid || projectID.Int64 != project.ID {
		t.Fatalf("registered run project_id = %v (valid=%t), want %v", projectID.Int64, projectID.Valid, project.ID)
	}
}

func TestRunRegisterUnknownCwdKeepsProjectNull(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if _, err := s.CreateProject(ctx, "atct", "/repos/atct"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	agentSessionID := runRegisterForTest(t, New(s), 0, "/outside/not-a-project")
	projectID := registeredAgentSessionProjectID(t, s, agentSessionID)
	if projectID.Valid {
		t.Fatalf("registered run project_id = %v, want NULL", projectID.Int64)
	}
}

func TestRunRegisterWithoutCwdKeepsProjectNull(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	agentSessionID := runRegisterForTest(t, New(s), 0, "")
	if agentSessionID == 0 {
		t.Fatal("run.register returned zero agent_session_id")
	}
	projectID := registeredAgentSessionProjectID(t, s, agentSessionID)
	if projectID.Valid {
		t.Fatalf("registered run project_id = %v, want NULL", projectID.Int64)
	}
}

func TestRunRegisterProjectScopeRejectsGoalFromAnotherProject(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	projectA, err := s.CreateProject(ctx, "project-a", "/repos/project-a")
	if err != nil {
		t.Fatalf("CreateProject(project-a): %v", err)
	}
	projectB, err := s.CreateProject(ctx, "project-b", "/repos/project-b")
	if err != nil {
		t.Fatalf("CreateProject(project-b): %v", err)
	}
	goal, err := s.CreateGoal(ctx, projectB.ID, "goal in project B", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	d := New(s)
	agentSessionID := runRegisterForTest(t, d, 0, projectA.RootPath)
	params, err := json.Marshal(map[string]any{
		"goal_id":          goal.ID,
		"agent_session_id": agentSessionID,
	})
	if err != nil {
		t.Fatalf("marshal goal.claim params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "goal.claim", Params: params}); err == nil || !strings.Contains(err.Error(), "agent session project scope violation") {
		t.Fatalf("goal.claim error = %v, want project scope violation", err)
	}
}

func TestSessionIdentifyReattachPreservesCanonicalProject(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	projectA, err := s.CreateProject(ctx, "project-a", "/repos/project-a")
	if err != nil {
		t.Fatalf("CreateProject(project-a): %v", err)
	}
	projectB, err := s.CreateProject(ctx, "project-b", "/repos/project-b")
	if err != nil {
		t.Fatalf("CreateProject(project-b): %v", err)
	}
	d := New(s)
	canonicalID := runRegisterForTest(t, d, 0, projectA.RootPath)
	identifyParams, err := json.Marshal(map[string]any{
		"agent_session_id": canonicalID,
		"session_key":      "stable-project-key",
	})
	if err != nil {
		t.Fatalf("marshal initial session.identify params: %v", err)
	}
	if _, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: identifyParams}); err != nil {
		t.Fatalf("initial session.identify: %v", err)
	}

	transportID := runRegisterForTest(t, d, 0, projectB.RootPath)
	identifyParams, err = json.Marshal(map[string]any{
		"agent_session_id": transportID,
		"session_key":      "stable-project-key",
	})
	if err != nil {
		t.Fatalf("marshal reattach session.identify params: %v", err)
	}
	result, err := d.dispatch(ctx, rpc.Request{Method: "session.identify", Params: identifyParams})
	if err != nil {
		t.Fatalf("reattach session.identify: %v", err)
	}
	var response struct {
		AgentSessionID int64 `json:"agent_session_id"`
		Reattached     bool  `json:"reattached"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal reattach response: %v", err)
	}
	if response.AgentSessionID != canonicalID || !response.Reattached {
		t.Fatalf("reattach response = (%d, %t), want (%d, true)", response.AgentSessionID, response.Reattached, canonicalID)
	}
	canonicalProjectID := registeredAgentSessionProjectID(t, s, canonicalID)
	if !canonicalProjectID.Valid || canonicalProjectID.Int64 != projectA.ID {
		t.Fatalf("canonical project_id = %v (valid=%t), want %v", canonicalProjectID.Int64, canonicalProjectID.Valid, projectA.ID)
	}
	transportProjectID := registeredAgentSessionProjectID(t, s, transportID)
	if !transportProjectID.Valid || transportProjectID.Int64 != projectB.ID {
		t.Fatalf("transport project_id = %v (valid=%t), want %v", transportProjectID.Int64, transportProjectID.Valid, projectB.ID)
	}
}

func runRegisterForTest(t *testing.T, d *Daemon, pid int, cwd string) int64 {
	t.Helper()
	params := map[string]any{"pid": pid}
	if cwd != "" {
		params["cwd"] = cwd
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal run.register params: %v", err)
	}
	result, err := d.dispatch(context.Background(), rpc.Request{Method: "run.register", Params: raw})
	if err != nil {
		t.Fatalf("run.register: %v", err)
	}
	var response struct {
		OK             bool  `json:"ok"`
		AgentSessionID int64 `json:"agent_session_id"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal run.register result: %v", err)
	}
	if !response.OK || response.AgentSessionID == 0 {
		t.Fatalf("run.register result = %v, want ok=true and a session id", result)
	}
	return response.AgentSessionID
}

func registeredAgentSessionProjectID(t *testing.T, s *store.Store, agentSessionID int64) sql.NullInt64 {
	t.Helper()
	var projectID sql.NullInt64
	if err := s.DB().QueryRowContext(context.Background(), `SELECT project_id FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&projectID); err != nil {
		t.Fatalf("lookup agent session project: %v", err)
	}
	return projectID
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
