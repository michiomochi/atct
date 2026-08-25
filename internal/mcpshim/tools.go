package mcpshim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GoalListIn struct {
	Cwd string `json:"cwd" jsonschema:"agent working directory; used to derive the project automatically"`
}

type GoalGetIn struct {
	GoalID string `json:"goal_id"`
}

type GoalClaimIn struct {
	GoalID string `json:"goal_id"`
}

type GoalReleaseIn struct {
	GoalID string `json:"goal_id"`
}

type ProjectClaimIn struct {
	ProjectID string `json:"project_id"`
}

type ProjectReleaseIn struct {
	ProjectID string `json:"project_id"`
}

type GoalUpdateContentIn struct {
	GoalID  string `json:"goal_id"`
	Content string `json:"content"`
}

type TaskUpdateContentIn struct {
	TaskID      string    `json:"task_id"`
	Title       *string   `json:"title,omitempty"`
	Description *string   `json:"description,omitempty"`
	Files       *[]string `json:"files,omitempty"`
}

type TaskDeclareIn struct {
	GoalID         string     `json:"goal_id"`
	Titles         []string   `json:"titles" jsonschema:"task titles decomposed from the goal, in execution order"`
	Descriptions   []string   `json:"descriptions" jsonschema:"task descriptions explaining the completion criteria and assumptions for each title, in execution order"`
	Files          [][]string `json:"files,omitempty" jsonschema:"files touched by each title, in the same order; paths are relative to the project root"`
	IdempotencyKey string     `json:"idempotency_key" jsonschema:"key that prevents duplicate tasks when the same declaration is retried"`
	Agent          string     `json:"agent"`
}

type TaskClaimIn struct {
	TaskID string `json:"task_id"`
}

type TaskReleaseIn struct {
	TaskID string `json:"task_id"`
}

type HandoffRequestIn struct {
	HandoffID     string `json:"handoff_id"`
	TaskID        string `json:"task_id"`
	RequestReport string `json:"request_report,omitempty"`
}

type HandoffReceiveIn struct {
	HandoffID string `json:"handoff_id,omitempty"`
	TaskID    string `json:"task_id"`
}

type HandoffCompleteIn struct {
	HandoffID      string `json:"handoff_id,omitempty"`
	TaskID         string `json:"task_id"`
	CompleteReport string `json:"complete_report,omitempty"`
}

type GoalHandoffRequestIn struct {
	HandoffID     string `json:"handoff_id"`
	GoalID        string `json:"goal_id"`
	RequestReport string `json:"request_report,omitempty"`
}

type GoalHandoffReceiveIn struct {
	HandoffID string `json:"handoff_id,omitempty"`
	GoalID    string `json:"goal_id"`
}

type GoalHandoffCompleteIn struct {
	HandoffID      string `json:"handoff_id,omitempty"`
	GoalID         string `json:"goal_id"`
	CompleteReport string `json:"complete_report,omitempty"`
}

type TaskUpdateIn struct {
	TaskID  string   `json:"task_id"`
	Status  string   `json:"status" jsonschema:"todo | doing | done | dropped"`
	Commits []string `json:"commits,omitempty" jsonschema:"commit SHAs produced by this task; optional"`
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
	GoalID      string `json:"goal_id"`
	WorkDone    string `json:"work_done" jsonschema:"what was completed; write なし when there is nothing to report"`
	NowPossible string `json:"now_possible" jsonschema:"what is possible now; write なし when there is nothing to report"`
	HowToVerify string `json:"how_to_verify" jsonschema:"how to verify the result; write なし when there is nothing to report"`
	Surprises   string `json:"surprises" jsonschema:"what differed from expectations; write なし when there is nothing to report"`
	NeedsReview string `json:"needs_review" jsonschema:"what still needs confirmation; write なし when there is nothing to report"`
	NextSteps   string `json:"next_steps" jsonschema:"what should happen next; write なし when there is nothing to report"`

	// ResultSummary keeps old Go callers compiling without exposing the removed
	// result_summary MCP argument.
	ResultSummary string `json:"-"`
}

type GoalSetDerivedFromIn struct {
	GoalID            string `json:"goal_id"`
	DerivedFromGoalID string `json:"derived_from_goal_id"`
}

type RoleIn struct {
	ExpectedRole string `json:"expected_role,omitempty" jsonschema:"optional expected role; a mismatch is returned in matches"`
}

type SessionIdentifyIn struct {
	SessionKey string `json:"session_key"`
}

type agentSessionIDHolder struct {
	mu sync.RWMutex
	id string
}

func (h *agentSessionIDHolder) Get() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.id
}

func (h *agentSessionIDHolder) Set(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.id = id
}

type roleResult struct {
	Role         string   `json:"role"`
	ProjectID    string   `json:"project_id"`
	GoalID       string   `json:"goal_id"`
	Does         []string `json:"does"`
	DoesNot      []string `json:"does_not"`
	ExpectedRole string   `json:"expected_role,omitempty"`
	Matches      *bool    `json:"matches,omitempty"`
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
	Role               string                    `json:"role,omitempty"`
	UnappliedDecisions []UnappliedDecisionNotice `json:"unapplied_decisions,omitempty"`
	ClaimableTasks     json.RawMessage           `json:"claimable_tasks,omitempty"`
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
			"role": map[string]any{"type": "string"},
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
			"claimable_tasks": map[string]any{
				"type":  "array",
				"items": map[string]any{},
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
		Role               string                    `json:"role"`
		UnappliedDecisions []UnappliedDecisionNotice `json:"unapplied_decisions"`
		ClaimableTasks     json.RawMessage           `json:"claimable_tasks"`
	}
	if err := json.Unmarshal(out, &envelope); err == nil && envelope.Data != nil {
		return nil, RawWithUnappliedDecisions{
			Data:               envelope.Data,
			Role:               envelope.Role,
			UnappliedDecisions: envelope.UnappliedDecisions,
			ClaimableTasks:     envelope.ClaimableTasks,
		}, nil
	}
	return nil, RawWithUnappliedDecisions{Data: out}, nil
}

func sessionRole(ctx context.Context, c *Client, agentSessionID string) (roleResult, error) {
	var response roleResult
	if err := c.Call(ctx, "session.role", map[string]any{"agent_session_id": strings.TrimSpace(agentSessionID)}, &response); err != nil {
		return roleResult{}, err
	}
	return response, nil
}

func callClaimWithRole(ctx context.Context, c *Client, method string, params any, agentSessionID string) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
	_, result, err := callWithUnappliedDecisions(ctx, c, method, params)
	if err != nil {
		return nil, RawWithUnappliedDecisions{}, err
	}
	response, err := sessionRole(ctx, c, agentSessionID)
	if err != nil {
		return nil, result, nil
	}
	result.Role = response.Role
	return nil, result, nil
}

func callRole(ctx context.Context, c *Client, in RoleIn, agentSessionID string) (*mcp.CallToolResult, Raw, error) {
	expectedRole := strings.TrimSpace(in.ExpectedRole)
	if expectedRole != "" && !validRole(expectedRole) {
		return nil, Raw{}, fmt.Errorf("expected_role must be one of commander, subcommander, executor")
	}

	response, err := sessionRole(ctx, c, agentSessionID)
	if err != nil {
		return nil, Raw{}, err
	}
	if expectedRole != "" {
		matches := response.Role == expectedRole
		response.ExpectedRole = expectedRole
		response.Matches = &matches
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, Raw{}, fmt.Errorf("marshal role response: %w", err)
	}
	return nil, Raw{Data: json.RawMessage(raw)}, nil
}

type sessionIdentifyResult struct {
	AgentSessionID string `json:"agent_session_id"`
	Reattached     bool   `json:"reattached"`
}

func callSessionIdentify(ctx context.Context, c *Client, in SessionIdentifyIn, agentSessionID *agentSessionIDHolder) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
	var response sessionIdentifyResult
	if err := c.Call(ctx, "session.identify", map[string]any{
		"agent_session_id": agentSessionID.Get(),
		"session_key":      strings.TrimSpace(in.SessionKey),
	}, &response); err != nil {
		return nil, RawWithUnappliedDecisions{}, err
	}
	if canonicalID := strings.TrimSpace(response.AgentSessionID); canonicalID != "" {
		agentSessionID.Set(canonicalID)
	}
	raw, err := json.Marshal(response)
	if err != nil {
		return nil, RawWithUnappliedDecisions{}, fmt.Errorf("marshal session identify response: %w", err)
	}
	return nil, RawWithUnappliedDecisions{Data: json.RawMessage(raw)}, nil
}

func validRole(role string) bool {
	switch role {
	case "commander", "subcommander", "executor":
		return true
	default:
		return false
	}
}

// Register adds agent-facing tools to the MCP server.
// Human operations (answer, approve, and reject) belong to the Web UI and are not exposed through MCP.
func Register(server *mcp.Server, c *Client, agentSessionID string) {
	sessionID := &agentSessionIDHolder{id: strings.TrimSpace(agentSessionID)}

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_session_identify",
		Description:  "Associate this transport with a stable caller-owned session key before using other atct tools.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in SessionIdentifyIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callSessionIdentify(ctx, c, in, sessionID)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_role",
		Description:  "Verify the current agent role and its project/goal claim evidence through the daemon. An optional expected_role is reported as matches; a mismatch is returned as structured data.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in RoleIn) (*mcp.CallToolResult, Raw, error) {
		return callRole(ctx, c, in, sessionID.Get())
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_project_claim",
		Description:  "Claim a project for this agent session. A live claim from another session is refused; a dead session's claim is taken over.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ProjectClaimIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callClaimWithRole(ctx, c, "project.claim", map[string]any{
			"project_id": in.ProjectID, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		}, sessionID.Get())
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_project_release",
		Description:  "Release the claim on a project.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in ProjectReleaseIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "project.release", map[string]any{
			"project_id": in.ProjectID, "agent_session_id": sessionID.Get(),
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_list",
		Description:  "Get active Goals and unapplied answers relevant to the current agent session. Call at startup and resume.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalListIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.list", map[string]any{
			"cwd": in.Cwd, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_get",
		Description:  "Get a goal's full content and all tasks, including done and dropped tasks.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalGetIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.get", map[string]any{
			"goal_id": in.GoalID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_claim",
		Description:  "Claim a goal for this agent session. A live claim from another session is refused; a dead session's claim is taken over.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalClaimIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callClaimWithRole(ctx, c, "goal.claim", map[string]any{
			"goal_id": in.GoalID, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		}, sessionID.Get())
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_release",
		Description:  "Release the claim on a goal.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalReleaseIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.release", map[string]any{
			"goal_id": in.GoalID,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_update_content",
		Description:  "Rewrite a proposed goal's content. Only a proposed goal can be rewritten; an approved goal (active, done, or dropped) is refused.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalUpdateContentIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.update_content", map[string]any{
			"goal_id": in.GoalID, "content": in.Content,
			"agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_declare",
		Description:  "Declare tasks decomposed from a Goal. Retrying the same idempotency_key does not create duplicates. Existing tasks are not updated and return with declared: false; use atct_task_update_content to fix them.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskDeclareIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "titles": in.Titles, "descriptions": in.Descriptions,
			"idempotency_key": in.IdempotencyKey, "agent": in.Agent,
			"agent_session_id":          sessionID.Get(),
			"include_unapplied_answers": true,
		}
		if in.Files != nil {
			params["files"] = in.Files
		}
		return callWithUnappliedDecisions(ctx, c, "task.declare", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_claim",
		Description:  "Claim a task for this agent session. Only one concurrent agent session can claim a task.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskClaimIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "task.claim", map[string]any{
			"task_id": in.TaskID, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_release",
		Description:  "Release the claim on a task.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskReleaseIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "task.release", map[string]any{
			"task_id": in.TaskID, "agent_session_id": sessionID.Get(),
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_handoff_request",
		Description:  "Request a task handoff. The task must have a live claim.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in HandoffRequestIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "handoff.request", map[string]any{
			"handoff_id": in.HandoffID, "task_id": in.TaskID, "requested_by": sessionID.Get(),
			"request_report": in.RequestReport,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_handoff_receive",
		Description:  "Record that a task handoff was received.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in HandoffReceiveIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"task_id": in.TaskID, "received_by": sessionID.Get(),
		}
		if in.HandoffID != "" {
			params["handoff_id"] = in.HandoffID
		}
		return callWithUnappliedDecisions(ctx, c, "handoff.receive", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_handoff_complete",
		Description:  "Report that a task handoff was completed.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in HandoffCompleteIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"task_id": in.TaskID, "complete_report": in.CompleteReport,
		}
		if in.HandoffID != "" {
			params["handoff_id"] = in.HandoffID
		}
		return callWithUnappliedDecisions(ctx, c, "handoff.complete", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_handoff_request",
		Description:  "Request a goal handoff. The goal must have a live claim.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalHandoffRequestIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.handoff.request", map[string]any{
			"handoff_id": in.HandoffID, "goal_id": in.GoalID, "requested_by": sessionID.Get(),
			"request_report": in.RequestReport,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_handoff_receive",
		Description:  "Record that a goal handoff was received.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalHandoffReceiveIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "received_by": sessionID.Get(),
		}
		if in.HandoffID != "" {
			params["handoff_id"] = in.HandoffID
		}
		return callWithUnappliedDecisions(ctx, c, "goal.handoff.receive", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_handoff_complete",
		Description:  "Report that a goal handoff was completed.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalHandoffCompleteIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "complete_report": in.CompleteReport,
		}
		if in.HandoffID != "" {
			params["handoff_id"] = in.HandoffID
		}
		return callWithUnappliedDecisions(ctx, c, "goal.handoff.complete", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_update",
		Description:  "Change a task status. Setting todo, done, or dropped releases the claim; a task with an open Decision cannot become done. Optionally pass the commit SHAs this task produced.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskUpdateIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "task.update", map[string]any{
			"task_id": in.TaskID, "status": in.Status, "agent_session_id": sessionID.Get(),
			"commits":                   in.Commits,
			"include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_task_update_content",
		Description:  "Rewrite a task's content. Only todo and doing tasks can be updated; done and dropped tasks are refused.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in TaskUpdateContentIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"task_id": in.TaskID, "agent_session_id": sessionID.Get(),
			"include_unapplied_answers": true,
		}
		if in.Title != nil {
			params["title"] = *in.Title
		}
		if in.Description != nil {
			params["description"] = *in.Description
		}
		if in.Files != nil {
			params["files"] = *in.Files
		}
		return callWithUnappliedDecisions(ctx, c, "task.update_content", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_ask",
		Description: "Ask the human for a decision. An answer received within wait_ms is returned." +
			"If parked is returned, continue with another task that does not depend on this decision.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionAskIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		params := map[string]any{
			"goal_id": in.GoalID, "task_id": in.TaskID, "question": in.Question,
			"options": in.Options, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
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
		return callWithUnappliedDecisions(ctx, c, "decision.ask", params)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_decision_poll",
		Description:  "Fetch the answer to a declared decision. Fetching transitions it to applied.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionPollIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "decision.poll", map[string]any{
			"agent_session_id": sessionID.Get(), "decision_id": in.DecisionID, "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: "atct_decision_withdraw",
		Description: "Withdraw a decision that is no longer needed. Always call this after resolving it independently." +
			"Otherwise stale questions remain in the human inbox.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in DecisionWithdrawIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "decision.withdraw", map[string]any{
			"decision_id": in.DecisionID, "reason": in.Reason, "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_complete",
		Description:  "Report goal completion and request human approval. Fails while an open Decision remains.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalCompleteIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.complete", map[string]any{
			"goal_id": in.GoalID, "work_done": in.WorkDone,
			"now_possible": in.NowPossible, "how_to_verify": in.HowToVerify,
			"surprises": in.Surprises, "needs_review": in.NeedsReview,
			"next_steps": in.NextSteps, "agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		})
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:         "atct_goal_set_derived_from",
		Description:  "Set or clear the goal from which this Goal was derived. Pass an empty derived_from_goal_id to clear it. Self-reference and cycles are rejected.",
		OutputSchema: rawOutputSchemaWithUnappliedDecisions(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in GoalSetDerivedFromIn) (*mcp.CallToolResult, RawWithUnappliedDecisions, error) {
		return callWithUnappliedDecisions(ctx, c, "goal.set_derived_from", map[string]any{
			"goal_id": in.GoalID, "derived_from_goal_id": in.DerivedFromGoalID,
			"agent_session_id": sessionID.Get(), "include_unapplied_answers": true,
		})
	})
}
