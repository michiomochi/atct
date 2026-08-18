package mcpshim

import (
	"context"
	"encoding/json"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GoalListIn struct {
	Cwd string `json:"cwd" jsonschema:"agent working directory; used to derive the project automatically"`
}

type TaskDeclareIn struct {
	GoalID         string     `json:"goal_id"`
	Titles         []string   `json:"titles" jsonschema:"task titles decomposed from the goal, in execution order"`
	Files          [][]string `json:"files,omitempty" jsonschema:"files touched by each title, in the same order; paths are relative to the project root"`
	IdempotencyKey string     `json:"idempotency_key" jsonschema:"key that prevents duplicate tasks when the same declaration is retried"`
	Agent          string     `json:"agent"`
}

type TaskClaimIn struct {
	TaskID string `json:"task_id"`
}

type TaskUpdateIn struct {
	TaskID string `json:"task_id"`
	Status string `json:"status" jsonschema:"todo | doing | done | dropped"`
}

type DecisionAskIn struct {
	GoalID         string          `json:"goal_id"`
	TaskID         string          `json:"task_id,omitempty"`
	Question       string          `json:"question" jsonschema:"describe the decision required from the human"`
	Options        []domain.Option `json:"options" jsonschema:"options; explain the consequence of each choice; may be empty"`
	DefaultOption  string          `json:"default_option,omitempty" jsonschema:"option label to apply after the timeout; must match one of the option labels"`
	DefaultAfterMs *int64          `json:"default_after_ms,omitempty" jsonschema:"milliseconds after which the default option is applied; 0 applies immediately"`
	WaitMs         *int            `json:"wait_ms,omitempty" jsonschema:"milliseconds to wait for an answer; defaults to 30000; returns parked after timeout"`
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
	Data any `json:"data"`
}

type UnappliedDecisionNotice struct {
	DecisionID string `json:"decision_id"`
	Question   string `json:"question"`
}

type RawWithUnappliedDecisions struct {
	Data               any                       `json:"data"`
	UnappliedDecisions []UnappliedDecisionNotice `json:"unapplied_decisions,omitempty"`
}

func rawOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{},
		},
		"required": []string{"data"},
	}
}

func rawOutputSchemaWithUnappliedDecisions() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{},
			"unapplied_decisions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"decision_id": map[string]any{"type": "string"},
						"question":    map[string]any{"type": "string"},
					},
					"required": []string{"decision_id", "question"},
				},
			},
		},
		"required": []string{"data"},
	}
}

func call(ctx context.Context, c *Client, method string, params any) (*mcp.CallToolResult, Raw, error) {
	var out json.RawMessage
	if err := c.Call(ctx, method, params, &out); err != nil {
		return nil, Raw{}, err
	}
	return nil, Raw{Data: out}, nil
}

func callWithUnappliedDecisions(ctx context.Context, c *Client, method string, params any) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
	var out json.RawMessage
	if err := c.Call(ctx, method, params, &out); err != nil {
		return nil, RawWithUnappliedDecisions{}, err
	}

	var envelope struct {
		Data               json.RawMessage           `json:"data"`
		UnappliedDecisions []UnappliedDecisionNotice `json:"unapplied_decisions"`
	}
	if err := json.Unmarshal(out, &envelope); err == nil && envelope.Data != nil {
		return nil, RawWithUnappliedDecisions{
			Data:               envelope.Data,
			UnappliedDecisions: envelope.UnappliedDecisions,
		}, nil
	}
	return nil, RawWithUnappliedDecisions{Data: out}, nil
}

// Register adds eight agent-facing tools to the MCP server.
// Human operations (answer, approve, reject, and stale-claim release) belong to the Web UI and are not exposed through MCP.
func Register(server *mcp.Server, c *Client, runID string) {
	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_list",
		Description:  "Get active Goals and unapplied answers relevant to the current run. Call at startup and resume.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalListIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.list", map[string]any{
			"cwd": in.Cwd, "run_id": runID, "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_declare",
		Description:  "Declare tasks decomposed from a Goal. Retrying the same idempotency_key does not create duplicates.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskDeclareIn) (*mcp.CallToolResult, Raw, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "titles": in.Titles,
			"idempotency_key": in.IdempotencyKey, "agent": in.Agent,
		}
		if in.Files != nil {
			params["files"] = in.Files
		}
		return call(ctx, c, "task.declare", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_claim",
		Description:  "Claim a task for this run. Only one concurrent run can claim a task.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskClaimIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "task.claim", map[string]any{
			"task_id": in.TaskID, "run_id": runID, "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_update",
		Description:  "Change a task status. Setting todo, done, or dropped releases the claim; a task with an open Decision cannot become done.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskUpdateIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "task.update", map[string]any{"task_id": in.TaskID, "status": in.Status})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_ask",
		Description: "Ask the human for a decision. An answer received within wait_ms is returned." +
			"If parked is returned, continue with another task that does not depend on this decision.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionAskIn) (*mcp.CallToolResult, Raw, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "task_id": in.TaskID, "question": in.Question,
			"options": in.Options, "run_id": runID,
		}
		if in.DefaultOption != "" {
			params["default_option"] = in.DefaultOption
		}
		if in.DefaultAfterMs != nil {
			params["default_after_ms"] = *in.DefaultAfterMs
		}
		if in.WaitMs != nil {
			params["wait_ms"] = *in.WaitMs
		}
		return call(ctx, c, "decision.ask", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_decision_poll",
		Description:  "Fetch the answer to a declared decision. Fetching transitions it to applied.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionPollIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "decision.poll", map[string]any{
			"run_id": runID, "decision_id": in.DecisionID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_withdraw",
		Description: "Withdraw a decision that is no longer needed. Always call this after resolving it independently." +
			"Otherwise stale questions remain in the human inbox.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionWithdrawIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "decision.withdraw", map[string]any{
			"decision_id": in.DecisionID, "reason": in.Reason,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_complete",
		Description:  "Report goal completion and request human approval. Fails while an open Decision remains.",
		OutputSchema: rawOutputSchema(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalCompleteIn) (*mcp.CallToolResult, Raw, error) {
		return call(ctx, c, "goal.complete", map[string]any{
			"goal_id": in.GoalID, "result_summary": in.ResultSummary, "run_id": runID,
		})
	})
}
