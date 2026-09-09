package mcpshim_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTaskCreateHandoffLifecycleToolsExecuteAgainstDaemon(t *testing.T) {
	ctx := context.Background()
	s, socketPath, goalID, handoffID, receiverID := taskCreateMCPFixture(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "atct-test", Version: "test"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(socketPath), receiverID)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "schema-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer clientSession.Close()

	createArgs := map[string]any{"goal_id": goalID, "agent": "worker", "titles": []string{"implementation"}, "descriptions": []string{"implement"}, "idempotency_key": "task-create"}
	var result *mcp.CallToolResult

	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "atct_task_create_handoff_receive", Arguments: map[string]any{"handoff_id": handoffID}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("atct_task_create_handoff_receive = %+v, %v", result, err)
	}
	handoff, err := s.GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
	}
	if handoff.ReceivedBy != receiverID {
		t.Fatalf("MCP receipt owner = %d, want injected session %d", handoff.ReceivedBy, receiverID)
	}

	createArgs["handoff_id"] = handoffID
	result, err = clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "atct_task_create", Arguments: createArgs})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("atct_task_create = %+v, %v", result, err)
	}
	handoff, err = s.GetTaskCreateHandoffForGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal after create: %v", err)
	}
	if handoff.CompletedBy != receiverID || handoff.CompletedAt == nil {
		t.Fatalf("MCP creation completion = %+v, want injected session %d without task delegation", handoff, receiverID)
	}
}

func taskCreateMCPFixture(t *testing.T) (*store.Store, string, int64, string, int64) {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct-task-create-mcp-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("store.Open: %v", err)
	}
	ctx := context.Background()
	project, err := s.CreateProject(ctx, "atct", filepath.Join(dir, "repo"))
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "task create", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(commander): %v", err)
	}
	receiverID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession(receiver): %v", err)
	}
	if err := s.AssociateAgentSessionWithProject(ctx, commanderID, project.ID); err != nil {
		t.Fatalf("AssociateAgentSessionWithProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	goalHandoff, err := s.RequestGoalHandoff(ctx, "mcp-goal", goal.ID, commanderID, "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, goalHandoff.ID, goal.ID, receiverID); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}
	plan, err := s.RequestPlanHandoffReview(ctx, "mcp-plan", goal.ID, receiverID, "ready")
	if err != nil {
		t.Fatalf("RequestPlanHandoffReview: %v", err)
	}
	if _, err := s.ReceivePlanHandoffReview(ctx, plan.ID, goal.ID, commanderID); err != nil {
		t.Fatalf("ReceivePlanHandoffReview: %v", err)
	}
	if _, err := s.CompletePlanHandoff(ctx, plan.ID, goal.ID, commanderID, "accepted"); err != nil {
		t.Fatalf("CompletePlanHandoff: %v", err)
	}
	handoff, err := s.GetTaskCreateHandoffForGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetTaskCreateHandoffForGoal: %v", err)
	}
	socketPath := filepath.Join(dir, "daemon.sock")
	serveCtx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- daemon.New(s).Serve(serveCtx, socketPath) }()
	for i := 0; i < 100; i++ {
		conn, dialErr := net.Dial("unix", socketPath)
		if dialErr == nil {
			conn.Close()
			break
		}
		select {
		case serveErr := <-serveDone:
			t.Fatalf("daemon.Serve exited before socket appeared: %v", serveErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
				t.Errorf("daemon.Serve: %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("daemon.Serve did not stop")
		}
		if err := s.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
		os.RemoveAll(dir)
	})
	return s, socketPath, goal.ID, handoff.ID, receiverID
}
