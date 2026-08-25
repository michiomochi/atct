package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

var ErrTaskAlreadyClaimed = errors.New("task already claimed")
var ErrGoalAlreadyClaimed = errors.New("goal already claimed")
var ErrProjectAlreadyClaimed = errors.New("project already claimed")
var ErrGoalNotProposed = errors.New("goal is not proposed")

type responseWithUnappliedDecisions struct {
	Data               any                             `json:"data"`
	UnappliedDecisions []unappliedDecisionNotification `json:"unapplied_decisions,omitempty"`
	ClaimableTasks     []claimableTaskSummary          `json:"claimable_tasks,omitempty"`
}

type sessionRoleResponse struct {
	Role      string   `json:"role"`
	ProjectID string   `json:"project_id"`
	GoalID    string   `json:"goal_id"`
	Does      []string `json:"does"`
	DoesNot   []string `json:"does_not"`
}

type roleBoundary struct {
	Does    []string
	DoesNot []string
}

var roleBoundaries = map[string]roleBoundary{
	"commander":    {Does: []string{"triage incoming work", "split goals", "prepare a working area", "review landed changes", "publish", "resolve conflicts", "clean up"}, DoesNot: []string{"design the goal", "implement the goal", "edit executor deliverables"}},
	"subcommander": {Does: []string{"design the goal", "delegate the goal's work", "review implementation", "report completion for the goal", "issue decisions to the human"}, DoesNot: []string{"inspect or manage other goals", "publish", "create another subcommander", "claim the project"}},
	"executor":     {Does: []string{"implement", "test"}, DoesNot: []string{"make design decisions", "re-delegate", "commit", "write internal version-control details"}},
}

type claimableTaskSummary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	GoalID string `json:"goal_id"`
}

func taskHandoffClaimedBy(handoff *store.TaskHandoff) string {
	if handoff == nil {
		return ""
	}
	if handoff.ReceivedBy != "" {
		return handoff.ReceivedBy
	}
	return handoff.RequestedBy
}

func taskHandoffClaimedAt(handoff *store.TaskHandoff) *time.Time {
	if handoff == nil {
		return nil
	}
	if handoff.ReceivedAt != nil {
		return handoff.ReceivedAt
	}
	return handoff.RequestedAt
}

func goalHandoffClaimedBy(handoff *store.GoalHandoff) string {
	if handoff == nil {
		return ""
	}
	if handoff.ReceivedBy != "" {
		return handoff.ReceivedBy
	}
	return handoff.RequestedBy
}

type goalListTaskCounts struct {
	Todo    int `json:"todo"`
	Doing   int `json:"doing"`
	Done    int `json:"done"`
	Dropped int `json:"dropped"`
}

func goalTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		if len(runes) > 120 {
			return string(runes[:120]) + "…"
		}
		return line
	}
	return ""
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

func (d *Daemon) listClaimableTasks(ctx context.Context, projectID, excludedTaskID string) ([]claimableTaskSummary, error) {
	goals, err := d.store.ListGoals(ctx, projectID)
	if err != nil {
		return nil, err
	}
	claimable := make([]claimableTaskSummary, 0, 3)
	for _, goal := range goals {
		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		handoffs, err := d.store.ListOpenTaskHandoffsForGoal(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if task.ID == excludedTaskID || task.Status != domain.TaskTodo || handoffs[task.ID] != nil {
				continue
			}
			claimable = append(claimable, claimableTaskSummary{
				ID: task.ID, Title: task.Title, GoalID: task.GoalID,
			})
			if len(claimable) == 3 {
				return claimable, nil
			}
		}
	}
	return claimable, nil
}

func tasksDeclaredWithIdempotencyKey(tasks []domain.Task, idempotencyKey string) []domain.Task {
	var declared []domain.Task
	for _, task := range tasks {
		separator := strings.LastIndex(task.DeclareKey, "#")
		if separator < 0 || task.DeclareKey[:separator] != idempotencyKey {
			continue
		}
		declared = append(declared, task)
	}
	return declared
}

func (d *Daemon) ensureAgentSessionProject(ctx context.Context, agentSessionID, targetProjectID string) error {
	if strings.TrimSpace(agentSessionID) == "" {
		return nil
	}
	assignedProjectID, err := d.store.ProjectIDForAgentSession(ctx, agentSessionID)
	if err != nil {
		if errors.Is(err, store.ErrAgentSessionNotRegistered) {
			if err := d.store.RegisterAgentSession(ctx, agentSessionID, 0); err != nil {
				return err
			}
			return d.store.AssociateAgentSessionWithProject(ctx, agentSessionID, targetProjectID)
		}
		if errors.Is(err, store.ErrAgentSessionNotAssociated) {
			return d.store.AssociateAgentSessionWithProject(ctx, agentSessionID, targetProjectID)
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
	return fmt.Errorf("agent session project scope violation: assigned project %q, target project %q", assignedProjectName, targetProjectName)
}

func (d *Daemon) resolveOrRegisterProject(ctx context.Context, cwd string) (domain.Project, error) {
	project, err := d.store.ResolveProject(ctx, cwd)
	if err == nil {
		return project, nil
	}
	if !errors.Is(err, store.ErrProjectNotFound) {
		return domain.Project{}, err
	}

	rootPath := store.NormalizeRoot(ctx, cwd)
	project, err = d.store.CreateProject(ctx, filepath.Base(rootPath), rootPath)
	if err == nil {
		return project, nil
	}

	project, resolveErr := d.store.ResolveProject(ctx, cwd)
	if resolveErr == nil {
		return project, nil
	}
	if !errors.Is(resolveErr, store.ErrProjectNotFound) {
		return domain.Project{}, resolveErr
	}

	fallbackName := filepath.Join(filepath.Base(filepath.Dir(rootPath)), filepath.Base(rootPath))
	return d.store.CreateProject(ctx, fallbackName, rootPath)
}

func (d *Daemon) dispatch(ctx context.Context, req rpc.Request) (json.RawMessage, error) {
	switch req.Method {
	case "run.register":
		var p struct {
			AgentSessionID string `json:"agent_session_id"`
			PID            int    `json:"pid"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		if err := d.store.RegisterAgentSession(ctx, p.AgentSessionID, p.PID); err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true}, nil)

	case "session.identify":
		var p struct {
			AgentSessionID string `json:"agent_session_id"`
			SessionKey     string `json:"session_key"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		canonicalID, reattached, err := d.store.IdentifyAgentSession(ctx, p.AgentSessionID, p.SessionKey)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{
			"agent_session_id": canonicalID,
			"reattached":       reattached,
		}, nil)

	case "session.role":
		var p struct {
			AgentSessionID string `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}

		response := sessionRoleResponse{Role: "executor"}
		if sessionID := strings.TrimSpace(p.AgentSessionID); sessionID != "" {
			projects, err := d.store.ListProjects(ctx)
			if err != nil {
				return nil, err
			}
			for _, project := range projects {
				if strings.TrimSpace(project.ClaimedBy) == sessionID {
					response.Role = "commander"
					response.ProjectID = project.ID
					break
				}
			}

			goals, err := d.store.ListAllGoals(ctx)
			if err != nil {
				return nil, err
			}
			goalHandoffs, err := d.store.ListOpenGoalHandoffs(ctx)
			if err != nil {
				return nil, err
			}
			for _, goal := range goals {
				handoff := goalHandoffs[goal.ID]
				if handoff != nil && handoff.ReceivedAt != nil && strings.TrimSpace(goalHandoffClaimedBy(handoff)) == sessionID {
					response.GoalID = goal.ID
					break
				}
			}
			if response.Role != "commander" && response.GoalID != "" {
				response.Role = "subcommander"
			}
		}
		boundary := roleBoundaries[response.Role]
		response.Does = boundary.Does
		response.DoesNot = boundary.DoesNot
		return marshal(response, nil)

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

	case "project.claim":
		var p struct {
			ProjectID      string `json:"project_id"`
			AgentSessionID string `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, p.ProjectID); err != nil {
			return nil, err
		}
		claimed, err := d.store.ClaimProject(ctx, p.ProjectID, p.AgentSessionID)
		if errors.Is(err, store.ErrProjectAlreadyClaimed) {
			return nil, ErrProjectAlreadyClaimed
		}
		return marshal(claimed, err)

	case "project.release":
		var p struct {
			ProjectID      string `json:"project_id"`
			AgentSessionID string `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		projectID := strings.TrimSpace(p.ProjectID)
		agentSessionID := strings.TrimSpace(p.AgentSessionID)
		if agentSessionID == "" {
			return nil, fmt.Errorf("project release requires agent_session_id: caller is not bound to project %q", projectID)
		}
		projects, err := d.store.ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("project release authorization: list projects: %w", err)
		}
		isHolder := false
		for _, project := range projects {
			if project.ID == projectID {
				isHolder = strings.TrimSpace(project.ClaimedBy) == agentSessionID
				break
			}
		}
		callerProjectID, sessionProjectErr := d.store.ProjectIDForAgentSession(ctx, agentSessionID)
		isProjectBound := sessionProjectErr == nil && callerProjectID == projectID
		if !isHolder && !isProjectBound {
			if sessionProjectErr != nil {
				return nil, fmt.Errorf("project release denied: caller %q is not the holder and is not bound to project %q: %w", agentSessionID, projectID, sessionProjectErr)
			}
			return nil, fmt.Errorf("project release denied: caller %q is bound to project %q, not project %q", agentSessionID, callerProjectID, projectID)
		}
		err = d.store.ReleaseProject(ctx, projectID)
		return marshal(nil, err)

	case "goal.list":
		var p struct {
			Cwd                     string `json:"cwd"`
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		ns, err := d.resolveOrRegisterProject(ctx, p.Cwd)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(p.AgentSessionID) != "" {
			if err := d.store.AssociateAgentSessionWithProject(ctx, p.AgentSessionID, ns.ID); err != nil {
				return nil, err
			}
		}
		goals, err := d.store.ListGoals(ctx, ns.ID)
		if err != nil {
			return nil, err
		}
		goalHandoffs, err := d.store.ListOpenGoalHandoffs(ctx)
		if err != nil {
			return nil, err
		}
		type goalListTask struct {
			ID          string            `json:"id"`
			GoalID      string            `json:"goal_id"`
			Title       string            `json:"title"`
			Description string            `json:"description"`
			Status      domain.TaskStatus `json:"status"`
			ClaimedBy   string            `json:"claimed_by"`
			Order       int               `json:"order"`
		}
		type goalListResponse struct {
			ID                string             `json:"id"`
			DerivedFromGoalID string             `json:"derived_from_goal_id,omitempty"`
			Title             string             `json:"title"`
			ContentChars      int                `json:"content_chars"`
			TaskCounts        goalListTaskCounts `json:"task_counts"`
			Status            domain.GoalStatus  `json:"status"`
			ClaimedBy         string             `json:"claimed_by,omitempty"`
			CreatedAt         time.Time          `json:"created_at"`
			Tasks             []goalListTask     `json:"tasks"`
		}
		visibleGoals := make([]goalListResponse, 0, len(goals))
		awaitingApprovalCount := 0
		for _, goal := range goals {
			if goal.Status == domain.GoalDone || goal.Status == domain.GoalDropped {
				continue
			}
			openDecisions, err := d.store.ListOpenDecisions(ctx, goal.ID)
			if err != nil {
				return nil, err
			}
			awaitingApproval := false
			for _, decision := range openDecisions {
				if decision.Kind == domain.KindCompletion {
					awaitingApproval = true
					break
				}
			}
			if awaitingApproval {
				awaitingApprovalCount++
				continue
			}
			tasks, err := d.store.ListTasks(ctx, goal.ID)
			if err != nil {
				return nil, err
			}
			taskHandoffs, err := d.store.ListOpenTaskHandoffsForGoal(ctx, goal.ID)
			if err != nil {
				return nil, err
			}
			activeTasks := make([]goalListTask, 0, len(tasks))
			taskCounts := goalListTaskCounts{}
			for _, task := range tasks {
				switch task.Status {
				case domain.TaskTodo:
					taskCounts.Todo++
				case domain.TaskDoing:
					taskCounts.Doing++
				case domain.TaskDone:
					taskCounts.Done++
				case domain.TaskDropped:
					taskCounts.Dropped++
				}
				if task.Status != domain.TaskTodo && task.Status != domain.TaskDoing {
					continue
				}
				activeTasks = append(activeTasks, goalListTask{
					ID:          task.ID,
					GoalID:      task.GoalID,
					Title:       task.Title,
					Description: goalTitle(task.Description),
					Status:      task.Status,
					ClaimedBy:   taskHandoffClaimedBy(taskHandoffs[task.ID]),
					Order:       task.Order,
				})
			}
			visibleGoals = append(visibleGoals, goalListResponse{
				ID:                goal.ID,
				DerivedFromGoalID: goal.DerivedFromGoalID,
				Title:             goalTitle(goal.Content),
				ContentChars:      utf8.RuneCountInString(goal.Content),
				TaskCounts:        taskCounts,
				Status:            goal.Status,
				ClaimedBy:         goalHandoffClaimedBy(goalHandoffs[goal.ID]),
				CreatedAt:         goal.CreatedAt,
				Tasks:             activeTasks,
			})
		}
		// spec section 7: goal.list returns active Goals and unapplied answers together.
		// This return value lets a new session recover answers (spec section 8, paragraph 3).
		// Mark matching agent_session_id answers as applied; return others for reference only.
		mine, err := d.store.PollDecisions(ctx, p.AgentSessionID, "")
		if err != nil {
			return nil, err
		}
		orphaned, err := d.store.ListUnappliedDecisionsForProject(ctx, ns.ID)
		if err != nil {
			return nil, err
		}
		data := map[string]any{
			"project":                 ns,
			"goals":                   visibleGoals,
			"awaiting_approval_count": awaitingApprovalCount,
			"answered_decisions":      mine,
			"orphaned_decisions":      orphaned,
		}
		if !p.IncludeUnappliedAnswers {
			return marshal(data, nil)
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               data,
			UnappliedDecisions: unappliedDecisionNotifications(orphaned),
		}, nil)

	case "goal.get":
		var p struct {
			GoalID string `json:"goal_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		tasks, err := d.store.ListTasks(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{
			"goal":  goal,
			"tasks": tasks,
		}, nil)

	case "goal.create":
		var p struct {
			Cwd     string `json:"cwd"`
			Content string `json:"content"`
			Creator string `json:"creator"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		project, err := d.store.ResolveProject(ctx, p.Cwd)
		if err != nil {
			return nil, fmt.Errorf("project is not registered; run `atct project add` first: %w", err)
		}
		goal, err := d.store.CreateGoal(ctx, project.ID, p.Content, p.Creator)
		return marshal(goal, err)

	case "goal.claim":
		var p struct {
			GoalID                  string `json:"goal_id"`
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		claimed, err := d.store.ClaimGoal(ctx, p.GoalID, p.AgentSessionID)
		if errors.Is(err, store.ErrGoalAlreadyClaimed) {
			return nil, ErrGoalAlreadyClaimed
		}
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(claimed, err)
		}
		unapplied, err := d.store.ListUnappliedDecisionsForProject(ctx, goal.ProjectID)
		if err != nil {
			return nil, err
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               claimed,
			UnappliedDecisions: unappliedDecisionNotifications(unapplied),
		}, nil)

	case "goal.release":
		var p struct {
			GoalID string `json:"goal_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		err := d.store.ReleaseGoal(ctx, p.GoalID)
		return marshal(nil, err)

	case "goal.update_content":
		var p struct {
			GoalID                  string `json:"goal_id"`
			Content                 string `json:"content"`
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		updated, err := d.store.UpdateGoalContent(ctx, p.GoalID, p.Content)
		if errors.Is(err, store.ErrGoalNotProposed) {
			return nil, ErrGoalNotProposed
		}
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(updated, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, updated, p.GoalID)
		return marshal(response, err)

	case "task.declare":
		var p struct {
			GoalID                  string     `json:"goal_id"`
			Agent                   string     `json:"agent"`
			IdempotencyKey          string     `json:"idempotency_key"`
			Titles                  []string   `json:"titles"`
			Descriptions            []string   `json:"descriptions"`
			Files                   [][]string `json:"files"`
			AgentSessionID          string     `json:"agent_session_id"`
			IncludeUnappliedAnswers bool       `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		tasks, err := d.store.DeclareTasks(ctx, p.GoalID, p.Agent, p.IdempotencyKey, p.Titles, p.Descriptions, p.Files)
		if err == nil {
			tasks = tasksDeclaredWithIdempotencyKey(tasks, p.IdempotencyKey)
		}
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tasks, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, tasks, p.GoalID)
		return marshal(response, err)

	case "task.update":
		var p struct {
			TaskID                  string   `json:"task_id"`
			Status                  string   `json:"status"`
			Commits                 []string `json:"commits"`
			AgentSessionID          string   `json:"agent_session_id"`
			IncludeUnappliedAnswers bool     `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, targetProjectID); err != nil {
			return nil, err
		}
		st, err := domain.ParseTaskStatus(p.Status)
		if err != nil {
			return nil, err
		}
		if len(p.Commits) > 0 {
			projects, err := d.store.ListProjects(ctx)
			if err != nil {
				return nil, err
			}
			var projectRoot string
			for _, project := range projects {
				if project.ID == targetProjectID {
					projectRoot = project.RootPath
					break
				}
			}
			resolvedCommits := make([]domain.TaskCommit, 0, len(p.Commits))
			for _, sha := range p.Commits {
				commit, err := d.store.ResolveCommit(ctx, projectRoot, sha)
				if err != nil {
					return nil, err
				}
				resolvedCommits = append(resolvedCommits, commit)
			}
			for _, commit := range resolvedCommits {
				commit.CreatedAt = time.Now()
				if err := d.store.LinkTaskCommit(ctx, p.TaskID, commit); err != nil {
					return nil, err
				}
			}
		}
		tk, err := d.store.UpdateTask(ctx, p.TaskID, st, p.AgentSessionID)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tk, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, tk, tk.GoalID)
		return marshal(response, err)

	case "task.claim":
		var p struct {
			TaskID                  string `json:"task_id"`
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, targetProjectID); err != nil {
			return nil, err
		}
		tk, err := d.store.ClaimTask(ctx, p.TaskID, p.AgentSessionID)
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
			TaskID         string `json:"task_id"`
			AgentSessionID string `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		tk, err := d.store.ReleaseTaskAs(ctx, p.TaskID, p.AgentSessionID)
		return marshal(tk, err)

	case "handoff.request":
		var p struct {
			HandoffID     string `json:"handoff_id"`
			TaskID        string `json:"task_id"`
			RequestedBy   string `json:"requested_by"`
			RequestReport string `json:"request_report"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		handoff, err := d.store.RequestTaskHandoff(ctx, p.HandoffID, p.TaskID, p.RequestedBy, p.RequestReport)
		return marshal(handoff, err)

	case "handoff.receive":
		var p struct {
			HandoffID  string `json:"handoff_id"`
			TaskID     string `json:"task_id"`
			ReceivedBy string `json:"received_by"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		var handoff store.TaskHandoff
		var err error
		if p.HandoffID == "" {
			handoff, err = d.store.ReceiveTaskHandoffForTask(ctx, p.TaskID, p.ReceivedBy)
		} else {
			handoff, err = d.store.ReceiveTaskHandoff(ctx, p.HandoffID, p.TaskID, p.ReceivedBy)
		}
		return marshal(handoff, err)

	case "handoff.complete":
		var p struct {
			HandoffID      string `json:"handoff_id"`
			TaskID         string `json:"task_id"`
			CompleteReport string `json:"complete_report"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		var handoff store.TaskHandoff
		var err error
		if p.HandoffID == "" {
			handoff, err = d.store.CompleteTaskHandoffForTask(ctx, p.TaskID, p.CompleteReport)
		} else {
			handoff, err = d.store.CompleteTaskHandoff(ctx, p.HandoffID, p.TaskID, p.CompleteReport)
		}
		return marshal(handoff, err)

	case "handoff.yielded":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		handoffs, err := d.store.ListTaskHandoffs(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		for _, handoff := range handoffs {
			if handoff.ReceivedAt == nil || handoff.CompletedReportAt != nil {
				continue
			}
			goalID, err := d.store.GetTaskGoalID(ctx, p.TaskID)
			if err != nil {
				return nil, err
			}
			goal, err := d.store.GetGoal(ctx, goalID)
			if err != nil {
				return nil, err
			}
			d.store.PublishEvent(store.DecisionEvent{
				Name: store.EventHandoffYielded,
				Data: store.DetectionEvent{
					ProjectID: goal.ProjectID,
					GoalID:    goalID,
					TaskID:    p.TaskID,
				},
			})
			break
		}
		return marshal(nil, nil)

	case "goal.handoff.request":
		var p struct {
			HandoffID     string `json:"handoff_id"`
			GoalID        string `json:"goal_id"`
			RequestedBy   string `json:"requested_by"`
			RequestReport string `json:"request_report"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		handoff, err := d.store.RequestGoalHandoff(ctx, p.HandoffID, p.GoalID, p.RequestedBy, p.RequestReport)
		return marshal(handoff, err)

	case "goal.handoff.receive":
		var p struct {
			HandoffID  string `json:"handoff_id"`
			GoalID     string `json:"goal_id"`
			ReceivedBy string `json:"received_by"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		var handoff store.GoalHandoff
		var err error
		if p.HandoffID == "" {
			handoff, err = d.store.ReceiveGoalHandoffForGoal(ctx, p.GoalID, p.ReceivedBy)
		} else {
			handoff, err = d.store.ReceiveGoalHandoff(ctx, p.HandoffID, p.GoalID, p.ReceivedBy)
		}
		return marshal(handoff, err)

	case "goal.handoff.complete":
		var p struct {
			HandoffID      string `json:"handoff_id"`
			GoalID         string `json:"goal_id"`
			CompleteReport string `json:"complete_report"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		var handoff store.GoalHandoff
		var err error
		if p.HandoffID == "" {
			handoff, err = d.store.CompleteGoalHandoffForGoal(ctx, p.GoalID, p.CompleteReport)
		} else {
			handoff, err = d.store.CompleteGoalHandoff(ctx, p.HandoffID, p.GoalID, p.CompleteReport)
		}
		return marshal(handoff, err)

	case "decision.ask":
		var p struct {
			GoalID                  string          `json:"goal_id"`
			TaskID                  string          `json:"task_id"`
			Question                string          `json:"question"`
			Options                 []domain.Option `json:"options"`
			DefaultOption           string          `json:"default_option"`
			DefaultAfterMs          *int64          `json:"default_after_ms"`
			AgentSessionID          string          `json:"agent_session_id"`
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
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		if p.TaskID != "" {
			targetProjectID, err := d.store.ProjectIDForTask(ctx, p.TaskID)
			if err != nil {
				return nil, err
			}
			if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, targetProjectID); err != nil {
				return nil, err
			}
		}
		dec, err := d.store.AskDecision(ctx, store.AskInput{
			GoalID: p.GoalID, TaskID: p.TaskID, Kind: domain.KindDecision,
			Question: p.Question, Options: p.Options, DefaultOption: p.DefaultOption,
			DefaultAfterMs: p.DefaultAfterMs, AgentSessionID: p.AgentSessionID,
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
		applied, err := d.store.PollDecisions(ctx, p.AgentSessionID, dec.ID)
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
			AgentSessionID          string `json:"agent_session_id"`
			DecisionID              string `json:"decision_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		decs, err := d.store.PollDecisions(ctx, p.AgentSessionID, p.DecisionID)
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

	case "goal.set_derived_from":
		var p struct {
			GoalID                  string `json:"goal_id"`
			DerivedFromGoalID       string `json:"derived_from_goal_id"`
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		if err := d.store.SetGoalDerivedFrom(ctx, p.GoalID, p.DerivedFromGoalID); err != nil {
			return nil, err
		}
		goal, err = d.store.GetGoal(ctx, p.GoalID)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(goal, err)
		}
		response, err := d.responseWithProjectUnappliedDecisions(ctx, goal, p.GoalID)
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
			AgentSessionID          string `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		dec, err := d.store.CompleteGoalWithReport(ctx, p.GoalID, domain.CompletionReport{
			WorkDone:    p.WorkDone,
			NowPossible: p.NowPossible,
			HowToVerify: p.HowToVerify,
			Surprises:   p.Surprises,
			NeedsReview: p.NeedsReview,
			NextSteps:   p.NextSteps,
		}, p.AgentSessionID)
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
