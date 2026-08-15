package mcpshim

import (
	"context"
	"encoding/json"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GoalListIn struct {
	Cwd string `json:"cwd" jsonschema:"agent working directory; used to derive the namespace automatically"`
}

type TaskDeclareIn struct {
	GoalID         string   `json:"goal_id"`
	Titles         []string `json:"titles" jsonschema:"task titles decomposed from the goal, in execution order"`
	IdempotencyKey string   `json:"idempotency_key" jsonschema:"key that prevents duplicate tasks when the same declaration is retried"`
	Agent          string   `json:"agent"`
}

type TaskClaimIn struct {
	TaskID string `json:"task_id"`
}

type TaskUpdateIn struct {
	TaskID string `json:"task_id"`
	Status string `json:"status" jsonschema:"todo | doing | done | dropped"`
}

type DecisionAskIn struct {
	GoalID   string          `json:"goal_id"`
	TaskID   string          `json:"task_id,omitempty"`
	Question string          `json:"question" jsonschema:"describe the decision required from the human"`
	Options  []domain.Option `json:"options" jsonschema:"options; explain the consequence of each choice; may be empty"`
	WaitMs   int             `json:"wait_ms,omitempty" jsonschema:"milliseconds to wait for an answer; defaults to 30000; returns parked after timeout"`
}

type DecisionPollIn struct {
	DecisionID string `json:"decision_id,omitempty"`
}

type DecisionWithdrawIn struct {
	DecisionID string `json:"decision_id"`
	Reason     string `json:"reason"`
}

type GoalCompleteIn struct {
	GoalID        string `json:"goal_id"`
	ResultSummary string `json:"result_summary" jsonschema:"completion report for the human"`
}

type Raw struct {
	Data json.RawMessage `json:"data"`
}

func call(ctx context.Context, c *Client, method string, params any) (*mcp.CallToolResult, Raw, error) {
	var out json.RawMessage
	if err := c.Call(ctx, method, params, &out); err != nil {
		return nil, Raw{}, err
	}
	return nil, Raw{Data: out}, nil
}

// Register adds eight agent-facing tools to the MCP server.
// Human operations (answer, approve, reject, and stale-claim release) belong to the Web UI and are not exposed through MCP.
func Register(server *mcp.Server, c *Client, runID string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_goal_list",
		Description: "Get active Goals and unapplied answers relevant to the current run. Call at startup and resume.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalListIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "goal.list", map[string]any{"cwd": in.Cwd, "run_id": runID})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_task_declare",
		Description: "Declare tasks decomposed from a Goal. Retrying the same idempotency_key does not create duplicates.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskDeclareIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "task.declare", map[string]any{
			"goal_id": in.GoalID, "titles": in.Titles,
			"idempotency_key": in.IdempotencyKey, "agent": in.Agent,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_task_claim",
		Description: "Claim a task for this run. Only one concurrent run can claim a task.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskClaimIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "task.claim", map[string]any{
			"task_id": in.TaskID, "run_id": runID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_task_update",
		Description: "Change a task status. Setting todo, done, or dropped releases the claim; a task with an open Decision cannot become done.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskUpdateIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "task.update", map[string]any{"task_id": in.TaskID, "status": in.Status})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_ask",
		Description: "Ask the human for a decision. An answer received within wait_ms is returned." +
			"If parked is returned, continue with another task that does not depend on this decision.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionAskIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "decision.ask", map[string]any{
			"goal_id": in.GoalID, "task_id": in.TaskID, "question": in.Question,
			"options": in.Options, "wait_ms": in.WaitMs, "run_id": runID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_decision_poll",
		Description: "Fetch the answer to a declared decision. Fetching transitions it to applied.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionPollIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "decision.poll", map[string]any{
			"run_id": runID, "decision_id": in.DecisionID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_withdraw",
		Description: "Withdraw a decision that is no longer needed. Always call this after resolving it independently." +
			"Otherwise stale questions remain in the human inbox.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionWithdrawIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "decision.withdraw", map[string]any{
			"decision_id": in.DecisionID, "reason": in.Reason,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "atct_goal_complete",
		Description: "Report goal completion and request human approval. Fails while an open Decision remains.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalCompleteIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "goal.complete", map[string]any{
			"goal_id": in.GoalID, "result_summary": in.ResultSummary, "run_id": runID,
		})
	})
}
