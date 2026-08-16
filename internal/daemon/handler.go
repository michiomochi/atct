package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

var ErrTaskAlreadyClaimed = errors.New("task already claimed")

func (d *Daemon) dispatch(ctx context.Context, req rpc.Request) (json.RawMessage, error) {
	switch req.Method {
	case "project.create":
		var p struct {
			Name     string `json:"name"`
			RootPath string `json:"root_path"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		rootPath := store.NormalizeRoot(ctx, p.RootPath)
		name := p.Name
		if name == "" {
			name = filepath.Base(rootPath)
		}
		ns, err := d.store.CreateProject(ctx, name, rootPath)
		return marshal(ns, err)

	case "project.list":
		projects, err := d.store.ListProjects(ctx)
		return marshal(projects, err)

	case "goal.list":
		var p struct {
			Cwd   string `json:"cwd"`
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		ns, err := d.store.ResolveProject(ctx, p.Cwd)
		if err != nil {
			return nil, err
		}
		goals, err := d.store.ListGoals(ctx, ns.ID)
		if err != nil {
			return nil, err
		}
		// spec section 7: goal.list returns active Goals and unapplied answers together.
		// This return value lets a new session recover answers (spec section 8, paragraph 3).
		// Mark matching run_id answers as applied; return others for reference only.
		mine, err := d.store.PollDecisions(ctx, p.RunID, "")
		if err != nil {
			return nil, err
		}
		orphaned, err := d.store.ListUnappliedDecisions(ctx)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{
			"project":            ns,
			"goals":              goals,
			"answered_decisions": mine,
			"orphaned_decisions": orphaned,
		}, nil)

	case "goal.create":
		var p struct {
			Cwd         string `json:"cwd"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		project, err := d.store.ResolveProject(ctx, p.Cwd)
		if err != nil {
			return nil, fmt.Errorf("project is not registered; run `atct project add` first: %w", err)
		}
		goal, err := d.store.CreateGoal(ctx, project.ID, p.Title, p.Description)
		return marshal(goal, err)

	case "task.declare":
		var p struct {
			GoalID         string   `json:"goal_id"`
			Agent          string   `json:"agent"`
			IdempotencyKey string   `json:"idempotency_key"`
			Titles         []string `json:"titles"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		tasks, err := d.store.DeclareTasks(ctx, p.GoalID, p.Agent, p.IdempotencyKey, p.Titles)
		return marshal(tasks, err)

	case "task.update":
		var p struct {
			TaskID string `json:"task_id"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		st, err := domain.ParseTaskStatus(p.Status)
		if err != nil {
			return nil, err
		}
		tk, err := d.store.UpdateTask(ctx, p.TaskID, st)
		return marshal(tk, err)

	case "task.claim":
		var p struct {
			TaskID string `json:"task_id"`
			RunID  string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		tk, err := d.store.ClaimTask(ctx, p.TaskID, p.RunID)
		if errors.Is(err, store.ErrTaskAlreadyClaimed) {
			return nil, ErrTaskAlreadyClaimed
		}
		return marshal(tk, err)

	case "task.release":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		tk, err := d.store.ReleaseTask(ctx, p.TaskID)
		return marshal(tk, err)

	case "decision.ask":
		var p struct {
			GoalID   string          `json:"goal_id"`
			TaskID   string          `json:"task_id"`
			Question string          `json:"question"`
			Options  []domain.Option `json:"options"`
			RunID    string          `json:"run_id"`
			WaitMs   int             `json:"wait_ms"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		dec, err := d.store.AskDecision(ctx, store.AskInput{
			GoalID: p.GoalID, TaskID: p.TaskID, Kind: domain.KindDecision,
			Question: p.Question, Options: p.Options, RunID: p.RunID,
		})
		if err != nil {
			return nil, err
		}
		if p.WaitMs <= 0 {
			p.WaitMs = 30000
		}
		answered, ok, err := d.store.WaitForAnswer(ctx, dec.ID, time.Duration(p.WaitMs)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		if !ok {
			return marshal(map[string]any{"parked": true, "decision_id": dec.ID}, nil)
		}
		applied, err := d.store.PollDecisions(ctx, p.RunID, dec.ID)
		if err != nil {
			return nil, err
		}
		if len(applied) > 0 {
			answered = applied[0]
		}
		return marshal(map[string]any{"parked": false, "decision": answered}, nil)

	case "decision.poll":
		var p struct {
			RunID      string `json:"run_id"`
			DecisionID string `json:"decision_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		decs, err := d.store.PollDecisions(ctx, p.RunID, p.DecisionID)
		return marshal(decs, err)

	case "decision.withdraw":
		var p struct {
			DecisionID string `json:"decision_id"`
			Reason     string `json:"reason"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		if err := d.store.WithdrawDecision(ctx, p.DecisionID, p.Reason); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true}, nil)

	case "goal.complete":
		var p struct {
			GoalID        string `json:"goal_id"`
			ResultSummary string `json:"result_summary"`
			RunID         string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		dec, err := d.store.CompleteGoal(ctx, p.GoalID, p.ResultSummary, p.RunID)
		return marshal(dec, err)
	}
	return nil, fmt.Errorf("unknown method: %s", req.Method)
}

func marshal(v any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
