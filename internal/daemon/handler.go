package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

var ErrTaskAlreadyClaimed = errors.New("task already claimed")

type responseWithUnappliedDecisions struct {
	Data               any                             `json:"data"`
	UnappliedDecisions []unappliedDecisionNotification `json:"unapplied_decisions,omitempty"`
	ClaimableTasks     []domain.Task                   `json:"claimable_tasks,omitempty"`
}

type unappliedDecisionNotification struct {
	DecisionID string `json:"decision_id"`
	Question   string `json:"question"`
}

func unappliedDecisionNotifications(decisions []domain.Decision) []unappliedDecisionNotification {
	return unappliedDecisionNotificationsExcept(decisions)
}

func unappliedDecisionNotificationsExcept(decisions []domain.Decision, excludedIDs ...string) []unappliedDecisionNotification {
	if len(decisions) == 0 {
		return nil
	}
	excluded := make(map[string]struct{}, len(excludedIDs))
	for _, id := range excludedIDs {
		excluded[id] = struct{}{}
	}
	notices := make([]unappliedDecisionNotification, 0, len(decisions))
	for _, decision := range decisions {
		if _, ok := excluded[decision.ID]; ok {
			continue
		}
		notices = append(notices, unappliedDecisionNotification{
			DecisionID: decision.ID,
			Question:   decision.Question,
		})
	}
	return notices
}

func (d *Daemon) responseWithProjectUnappliedDecisions(ctx context.Context, data any, goalID string, excludedIDs ...string) (responseWithUnappliedDecisions, error) {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return responseWithUnappliedDecisions{}, err
	}
	unapplied, err := d.store.ListUnappliedDecisionsForProject(ctx, goal.ProjectID)
	if err != nil {
		return responseWithUnappliedDecisions{}, err
	}
	return responseWithUnappliedDecisions{
		Data:               data,
		UnappliedDecisions: unappliedDecisionNotificationsExcept(unapplied, excludedIDs...),
	}, nil
}

func (d *Daemon) listClaimableTasks(ctx context.Context, projectID, excludedTaskID string) ([]domain.Task, error) {
	goals, err := d.store.ListGoals(ctx, projectID)
	if err != nil {
		return nil, err
	}
	claimable := make([]domain.Task, 0, 3)
	for _, goal := range goals {
		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.ID == excludedTaskID || task.Status != domain.TaskTodo || strings.TrimSpace(task.ClaimedBy) != "" {
				continue
			}
			claimable = append(claimable, task)
			if len(claimable) == 3 {
				return claimable, nil
			}
		}
	}
	return claimable, nil
}

func (d *Daemon) ensureRunProject(ctx context.Context, runID, targetProjectID string) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	assignedProjectID, err := d.store.ProjectIDForRun(ctx, runID)
	if err != nil {
		if errors.Is(err, store.ErrRunNotRegistered) {
			if err := d.store.RegisterRun(ctx, runID); err != nil {
				return err
			}
			return d.store.AssociateRunWithProject(ctx, runID, targetProjectID)
		}
		if errors.Is(err, store.ErrRunNotAssociated) {
			return d.store.AssociateRunWithProject(ctx, runID, targetProjectID)
		}
		return err
	}
	if assignedProjectID == targetProjectID {
		return nil
	}

	projects, err := d.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	assignedProjectName := assignedProjectID
	targetProjectName := targetProjectID
	for _, project := range projects {
		switch project.ID {
		case assignedProjectID:
			assignedProjectName = project.Name
		case targetProjectID:
			targetProjectName = project.Name
		}
	}
	return fmt.Errorf("run project scope violation: assigned project %q, target project %q", assignedProjectName, targetProjectName)
}

func (d *Daemon) dispatch(ctx context.Context, req rpc.Request) (json.RawMessage, error) {
	switch req.Method {
	case "run.register":
		var p struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		if err := d.store.RegisterRun(ctx, p.RunID); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true}, nil)

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
			Cwd                     string `json:"cwd"`
			RunID                   string `json:"run_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		ns, err := d.store.ResolveProject(ctx, p.Cwd)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.RunID) != "" {
			if err := d.store.AssociateRunWithProject(ctx, p.RunID, ns.ID); err != nil {
				return nil, err
			}
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
		data := map[string]any{
			"project":            ns,
			"goals":              goals,
			"answered_decisions": mine,
			"orphaned_decisions": orphaned,
		}
		if !p.IncludeUnappliedAnswers {
			return marshal(data, nil)
		}
		unapplied, err := d.store.ListUnappliedDecisionsForProject(ctx, ns.ID)
		if err != nil {
			return nil, err
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               data,
			UnappliedDecisions: unappliedDecisionNotifications(unapplied),
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
			GoalID                  string     `json:"goal_id"`
			Agent                   string     `json:"agent"`
			IdempotencyKey          string     `json:"idempotency_key"`
			Titles                  []string   `json:"titles"`
			Files                   [][]string `json:"files"`
			RunID                   string     `json:"run_id"`
			IncludeUnappliedAnswers bool       `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureRunProject(ctx, p.RunID, goal.ProjectID); err != nil {
			return nil, err
		}
		tasks, err := d.store.DeclareTasks(ctx, p.GoalID, p.Agent, p.IdempotencyKey, p.Titles, p.Files)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tasks, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, tasks, p.GoalID)
		return marshal(response, err)

	case "task.update":
		var p struct {
			TaskID                  string `json:"task_id"`
			Status                  string `json:"status"`
			RunID                   string `json:"run_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureRunProject(ctx, p.RunID, targetProjectID); err != nil {
			return nil, err
		}
		st, err := domain.ParseTaskStatus(p.Status)
		if err != nil {
			return nil, err
		}
		tk, err := d.store.UpdateTask(ctx, p.TaskID, st)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tk, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, tk, tk.GoalID)
		return marshal(response, err)

	case "task.claim":
		var p struct {
			TaskID                  string `json:"task_id"`
			RunID                   string `json:"run_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureRunProject(ctx, p.RunID, targetProjectID); err != nil {
			return nil, err
		}
		tk, err := d.store.ClaimTask(ctx, p.TaskID, p.RunID)
		if errors.Is(err, store.ErrTaskAlreadyClaimed) {
			return nil, ErrTaskAlreadyClaimed
		}
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tk, err)
		}
		goal, err := d.store.GetGoal(ctx, tk.GoalID)
		if err != nil {
			return nil, err
		}
		unapplied, err := d.store.ListUnappliedDecisionsForProject(ctx, goal.ProjectID)
		if err != nil {
			return nil, err
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               tk,
			UnappliedDecisions: unappliedDecisionNotifications(unapplied),
		}, nil)

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
			GoalID                  string          `json:"goal_id"`
			TaskID                  string          `json:"task_id"`
			Question                string          `json:"question"`
			Options                 []domain.Option `json:"options"`
			DefaultOption           string          `json:"default_option"`
			DefaultAfterMs          *int64          `json:"default_after_ms"`
			RunID                   string          `json:"run_id"`
			WaitMs                  *int            `json:"wait_ms"`
			IncludeUnappliedAnswers bool            `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureRunProject(ctx, p.RunID, goal.ProjectID); err != nil {
			return nil, err
		}
		if p.TaskID != "" {
			targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
			if err != nil {
				return nil, err
			}
			if err := d.ensureRunProject(ctx, p.RunID, targetProjectID); err != nil {
				return nil, err
			}
		}
		dec, err := d.store.AskDecision(ctx, store.AskInput{
			GoalID: p.GoalID, TaskID: p.TaskID, Kind: domain.KindDecision,
			Question: p.Question, Options: p.Options, DefaultOption: p.DefaultOption,
			DefaultAfterMs: p.DefaultAfterMs, RunID: p.RunID,
		})
		if err != nil {
			return nil, err
		}
		waitMs := 30000
		if p.WaitMs != nil {
			waitMs = *p.WaitMs
		}
		if waitMs <= 0 {
			data := map[string]any{"parked": true, "decision_id": dec.ID}
			if !p.IncludeUnappliedAnswers {
				return marshal(data, nil)
			}
			response, err := d.responseWithProjectUnappliedDecisions(ctx, data, p.GoalID, dec.ID)
			if err != nil {
				return nil, err
			}
			goal, err := d.store.GetGoal(ctx, p.GoalID)
			if err != nil {
				return nil, err
			}
			response.ClaimableTasks, err = d.listClaimableTasks(ctx, goal.ProjectID, p.TaskID)
			return marshal(response, err)
		}
		answered, ok, err := d.store.WaitForAnswer(ctx, dec.ID, time.Duration(waitMs)*time.Millisecond)
		if err != nil {
			return nil, err
		}
		if !ok {
			data := map[string]any{"parked": true, "decision_id": dec.ID}
			if !p.IncludeUnappliedAnswers {
				return marshal(data, nil)
			}
			response, err := d.responseWithProjectUnappliedDecisions(ctx, data, p.GoalID, dec.ID)
			if err != nil {
				return nil, err
			}
			goal, err := d.store.GetGoal(ctx, p.GoalID)
			if err != nil {
				return nil, err
			}
			response.ClaimableTasks, err = d.listClaimableTasks(ctx, goal.ProjectID, p.TaskID)
			return marshal(response, err)
		}
		applied, err := d.store.PollDecisions(ctx, p.RunID, dec.ID)
		if err != nil {
			return nil, err
		}
		if len(applied) > 0 {
			answered = applied[0]
		}
		data := map[string]any{"parked": false, "decision": answered}
		if !p.IncludeUnappliedAnswers {
			return marshal(data, nil)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, data, p.GoalID, dec.ID)
		return marshal(response, err)

	case "decision.poll":
		var p struct {
			RunID                   string `json:"run_id"`
			DecisionID              string `json:"decision_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		decs, err := d.store.PollDecisions(ctx, p.RunID, p.DecisionID)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(decs, err)
		}
		goalID := ""
		if len(decs) > 0 {
			goalID = decs[0].GoalID
		} else if p.DecisionID != "" {
			decision, err := d.store.GetDecision(ctx, p.DecisionID)
			if err != nil {
				return nil, err
			}
			goalID = decision.GoalID
		}
		if goalID == "" {
			return marshal(responseWithUnappliedDecisions{Data: decs}, nil)
		}
		excludedIDs := make([]string, 0, len(decs))
		for _, decision := range decs {
			excludedIDs = append(excludedIDs, decision.ID)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, decs, goalID, excludedIDs...)
		return marshal(response, err)

	case "decision.withdraw":
		var p struct {
			DecisionID              string `json:"decision_id"`
			Reason                  string `json:"reason"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		var decision domain.Decision
		if p.IncludeUnappliedAnswers {
			var err error
			decision, err = d.store.GetDecision(ctx, p.DecisionID)
			if err != nil {
				return nil, err
			}
		}
		if err := d.store.WithdrawDecision(ctx, p.DecisionID, p.Reason); err != nil {
			return nil, err
		}
		data := map[string]any{"ok": true}
		if !p.IncludeUnappliedAnswers {
			return marshal(data, nil)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, data, decision.GoalID, decision.ID)
		return marshal(response, err)

	case "goal.complete":
		var p struct {
			GoalID                  string `json:"goal_id"`
			WorkDone                string `json:"work_done"`
			NowPossible             string `json:"now_possible"`
			HowToVerify             string `json:"how_to_verify"`
			Surprises               string `json:"surprises"`
			NeedsReview             string `json:"needs_review"`
			NextSteps               string `json:"next_steps"`
			RunID                   string `json:"run_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureRunProject(ctx, p.RunID, goal.ProjectID); err != nil {
			return nil, err
		}
		dec, err := d.store.CompleteGoalWithReport(ctx, p.GoalID, domain.CompletionReport{
			WorkDone:    p.WorkDone,
			NowPossible: p.NowPossible,
			HowToVerify: p.HowToVerify,
			Surprises:   p.Surprises,
			NeedsReview: p.NeedsReview,
			NextSteps:   p.NextSteps,
		}, p.RunID)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(dec, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, dec, p.GoalID)
		return marshal(response, err)
	}
	return nil, fmt.Errorf("unknown method: %s", req.Method)
}

func marshal(v any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
