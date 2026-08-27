package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
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
var ErrDecisionOutsideGoal = errors.New("decision belongs to another goal")

type responseWithUnappliedDecisions struct {
	Data               any                             `json:"data"`
	UnappliedDecisions []unappliedDecisionNotification `json:"unapplied_decisions,omitempty"`
	ClaimableTasks     []claimableTaskSummary          `json:"claimable_tasks,omitempty"`
}

type roleAssignment struct {
	Role      string
	ProjectID int64
	GoalID    int64
}

type commanderRole struct {
	Role      string   `json:"role"`
	ProjectID int64    `json:"project_id"`
	Does      []string `json:"does"`
	DoesNot   []string `json:"does_not"`
}

type subcommanderRole struct {
	Role    string   `json:"role"`
	GoalID  int64    `json:"goal_id"`
	Does    []string `json:"does"`
	DoesNot []string `json:"does_not"`
}

type executorRole struct {
	Role    string   `json:"role"`
	Does    []string `json:"does"`
	DoesNot []string `json:"does_not"`
}

type roleBoundary struct {
	Does    []string
	DoesNot []string
}

var roleBoundaries = map[string]roleBoundary{
	"commander":    {Does: []string{"triage incoming work", "split goals", "prepare a working area", "review landed changes", "publish", "resolve conflicts", "clean up"}, DoesNot: []string{"design the goal", "implement the goal", "edit executor deliverables"}},
	"subcommander": {Does: []string{"design the goal", "delegate the goal's work", "review implementation", "report completion for the goal", "issue decisions to the human", "commit the goal's work", "close a task its worker cannot"}, DoesNot: []string{"inspect or manage other goals", "publish", "create another subcommander", "claim the project"}},
	"executor":     {Does: []string{"implement", "test", "close the task it was given"}, DoesNot: []string{"make design decisions", "re-delegate", "commit", "write internal version-control details"}},
}

type claimableTaskSummary struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	GoalID int64  `json:"goal_id"`
}

func taskHandoffClaimedBy(handoff *store.TaskHandoff) int64 {
	if handoff == nil {
		return 0
	}
	if handoff.ReceivedBy != 0 {
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

func goalHandoffClaimedBy(handoff *store.GoalHandoff) int64 {
	if handoff == nil {
		return 0
	}
	if handoff.ReceivedBy != 0 {
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

func summaryLine(content string) string {
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
	DecisionID int64  `json:"decision_id"`
	Question   string `json:"question"`
}

func unappliedDecisionNotifications(decisions []domain.Decision) []unappliedDecisionNotification {
	return unappliedDecisionNotificationsExcept(decisions)
}

func unappliedDecisionNotificationsExcept(decisions []domain.Decision, excludedIDs ...int64) []unappliedDecisionNotification {
	if len(decisions) == 0 {
		return nil
	}
	excluded := make(map[int64]struct{}, len(excludedIDs))
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

func (d *Daemon) deriveSessionRole(ctx context.Context, agentSessionID int64) (roleAssignment, error) {
	response := roleAssignment{Role: "executor"}
	if agentSessionID != 0 {
		projects, err := d.store.ListProjects(ctx)
		if err != nil {
			return roleAssignment{}, err
		}
		for _, project := range projects {
			if project.ClaimedBy == agentSessionID {
				response.Role = "commander"
				response.ProjectID = project.ID
				break
			}
		}

		goals, err := d.store.ListAllGoals(ctx)
		if err != nil {
			return roleAssignment{}, err
		}
		goalHandoffs, err := d.store.ListOpenGoalHandoffs(ctx)
		if err != nil {
			return roleAssignment{}, err
		}
		for _, goal := range goals {
			handoff := goalHandoffs[goal.ID]
			if handoff != nil && handoff.ReceivedAt != nil && goalHandoffClaimedBy(handoff) == agentSessionID {
				response.GoalID = goal.ID
				break
			}
		}
		if response.Role != "commander" && response.GoalID != 0 {
			response.Role = "subcommander"
		}
	}
	return response, nil
}

func roleResponseFor(assignment roleAssignment) any {
	boundary := roleBoundaries[assignment.Role]
	switch assignment.Role {
	case "commander":
		return commanderRole{
			Role:      assignment.Role,
			ProjectID: assignment.ProjectID,
			Does:      boundary.Does,
			DoesNot:   boundary.DoesNot,
		}
	case "subcommander":
		return subcommanderRole{
			Role:    assignment.Role,
			GoalID:  assignment.GoalID,
			Does:    boundary.Does,
			DoesNot: boundary.DoesNot,
		}
	default:
		return executorRole{
			Role:    "executor",
			Does:    boundary.Does,
			DoesNot: boundary.DoesNot,
		}
	}
}

func (d *Daemon) unappliedDecisionsForSessionInProject(ctx context.Context, projectID int64, agentSessionID int64) ([]domain.Decision, error) {
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil {
		return nil, err
	}
	if role.Role == "subcommander" && role.GoalID != 0 {
		return d.store.ListUnappliedDecisionsForGoal(ctx, role.GoalID)
	}
	return d.store.ListUnappliedDecisionsForProject(ctx, projectID)
}

func (d *Daemon) unappliedDecisionsForSession(ctx context.Context, goalID int64, agentSessionID int64) ([]domain.Decision, error) {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return nil, err
	}
	return d.unappliedDecisionsForSessionInProject(ctx, goal.ProjectID, agentSessionID)
}

func (d *Daemon) responseWithScopedUnappliedDecisions(ctx context.Context, data any, goalID int64, agentSessionID int64, excludedIDs ...int64) (responseWithUnappliedDecisions, error) {
	unapplied, err := d.unappliedDecisionsForSession(ctx, goalID, agentSessionID)
	if err != nil {
		return responseWithUnappliedDecisions{}, err
	}
	return responseWithUnappliedDecisions{
		Data:               data,
		UnappliedDecisions: unappliedDecisionNotificationsExcept(unapplied, excludedIDs...),
	}, nil
}

func (d *Daemon) listClaimableTasks(ctx context.Context, projectID, excludedTaskID int64) ([]claimableTaskSummary, error) {
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

func (d *Daemon) ensureAgentSessionProject(ctx context.Context, agentSessionID int64, targetProjectID int64) error {
	if agentSessionID == 0 {
		return nil
	}
	assignedProjectID, err := d.store.ProjectIDForAgentSession(ctx, agentSessionID)
	if err != nil {
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
	assignedProjectName := fmt.Sprint(assignedProjectID)
	targetProjectName := fmt.Sprint(targetProjectID)
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

func (d *Daemon) authorizeGoalCompletion(ctx context.Context, goalID int64, projectID int64, agentSessionID int64) error {
	if agentSessionID == 0 {
		return fmt.Errorf("goal completion denied: goal %d requires agent_session_id; identify the session before reporting completion", goalID)
	}

	projects, err := d.store.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("goal completion authorization: list projects: %w", err)
	}
	for _, project := range projects {
		if project.ID == projectID && project.ClaimedBy == agentSessionID {
			return nil
		}
	}

	handoffs, err := d.store.ListOpenGoalHandoffs(ctx)
	if err != nil {
		return fmt.Errorf("goal completion authorization: list open goal handoffs: %w", err)
	}
	handoff := handoffs[goalID]
	if handoff == nil {
		return fmt.Errorf("goal completion denied: caller %d holds no open goal handoff for goal %d; receive the goal handoff for this goal or claim its project", agentSessionID, goalID)
	}
	if handoff.ReceivedAt == nil {
		return fmt.Errorf("goal completion denied: the goal handoff for goal %d was requested but never received; caller %d must receive it before reporting completion", goalID, agentSessionID)
	}
	if handoff.ReceivedBy != agentSessionID {
		return fmt.Errorf("goal completion denied: caller %d is not the holder of goal %d; actual holder is session %d", agentSessionID, goalID, handoff.ReceivedBy)
	}
	return nil
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

// normalizeEntityIDs keeps the transport compatible with clients that send
// numeric IDs as strings while the daemon's internal and response types use
// canonical integer IDs. Other strings are passed to the resolver so removed
// UUID-style IDs receive migration guidance. Numeric JSON values are validated
// here as well so a fractional value cannot be silently truncated later.
func (d *Daemon) normalizeEntityIDs(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	trimmedParams := strings.TrimSpace(string(params))
	if trimmedParams == "" || trimmedParams == "null" {
		return params, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return params, nil
	}

	resolvers := map[string]func(context.Context, string) (int64, error){
		"project_id":           d.store.ResolveProjectID,
		"goal_id":              d.store.ResolveGoalID,
		"task_id":              d.store.ResolveTaskID,
		"decision_id":          d.store.ResolveDecisionID,
		"derived_from_goal_id": d.store.ResolveGoalID,
	}
	for field, resolve := range resolvers {
		raw, ok := fields[field]
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" || trimmed == "null" {
			continue
		}

		if strings.HasPrefix(trimmed, `"`) {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, fmt.Errorf("%s must be an integer or string: %w", field, err)
			}
			if strings.TrimSpace(value) == "" && optionalEntityID(method, field) {
				fields[field] = json.RawMessage("0")
				continue
			}
			id, err := resolve(ctx, value)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", field, err)
			}
			fields[field] = json.RawMessage(strconv.FormatInt(id, 10))
			continue
		}

		var id int64
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, fmt.Errorf("%s must be an integer or string: %w", field, err)
		}
		fields[field] = json.RawMessage(strconv.FormatInt(id, 10))
	}
	for _, field := range []string{"agent_session_id", "requested_by", "received_by"} {
		raw, ok := fields[field]
		if !ok {
			continue
		}
		id, err := parseAgentSessionID(raw)
		if err != nil {
			return nil, fmt.Errorf("%s must be an integer or decimal string: %w", field, err)
		}
		fields[field] = json.RawMessage(strconv.FormatInt(id, 10))
	}
	return json.Marshal(fields)
}

func parseAgentSessionID(raw json.RawMessage) (int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return 0, nil
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, err
		}
		return id, nil
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, err
	}
	return id, nil
}

func optionalEntityID(method, field string) bool {
	switch {
	case method == "decision.ask" && field == "task_id":
		return true
	case method == "decision.poll" && field == "decision_id":
		return true
	case method == "goal.set_derived_from" && field == "derived_from_goal_id":
		return true
	default:
		return false
	}
}

func (d *Daemon) dispatch(ctx context.Context, req rpc.Request) (json.RawMessage, error) {
	params, err := d.normalizeEntityIDs(ctx, req.Method, req.Params)
	if err != nil {
		return nil, err
	}
	req.Params = params

	switch req.Method {
	case "run.register":
		var p struct {
			PID int `json:"pid"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		agentSessionID, err := d.store.RegisterAgentSession(ctx, p.PID)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"ok": true, "agent_session_id": agentSessionID}, nil)

	case "session.identify":
		var p struct {
			AgentSessionID int64  `json:"agent_session_id"`
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
			AgentSessionID int64 `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}

		response, err := d.deriveSessionRole(ctx, p.AgentSessionID)
		if err != nil {
			return nil, err
		}
		return marshal(roleResponseFor(response), nil)

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
			ProjectID      int64 `json:"project_id"`
			AgentSessionID int64 `json:"agent_session_id"`
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
			ProjectID      int64 `json:"project_id"`
			AgentSessionID int64 `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		projectID := p.ProjectID
		agentSessionID := p.AgentSessionID
		if agentSessionID == 0 {
			return nil, fmt.Errorf("project release requires agent_session_id: caller is not bound to project %d", projectID)
		}
		projects, err := d.store.ListProjects(ctx)
		if err != nil {
			return nil, fmt.Errorf("project release authorization: list projects: %w", err)
		}
		isHolder := false
		for _, project := range projects {
			if project.ID == projectID {
				isHolder = project.ClaimedBy == agentSessionID
				break
			}
		}
		callerProjectID, sessionProjectErr := d.store.ProjectIDForAgentSession(ctx, agentSessionID)
		isProjectBound := sessionProjectErr == nil && callerProjectID == projectID
		if !isHolder && !isProjectBound {
			if sessionProjectErr != nil {
				return nil, fmt.Errorf("project release denied: caller %d is not the holder and is not bound to project %d: %w", agentSessionID, projectID, sessionProjectErr)
			}
			return nil, fmt.Errorf("project release denied: caller %d is bound to project %d, not project %d", agentSessionID, callerProjectID, projectID)
		}
		err = d.store.ReleaseProject(ctx, projectID)
		return marshal(nil, err)

	case "goal.list":
		var p struct {
			Cwd                     string `json:"cwd"`
			AgentSessionID          int64  `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		ns, err := d.resolveOrRegisterProject(ctx, p.Cwd)
		if err != nil {
			return nil, err
		}
		if p.AgentSessionID != 0 {
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
			ID          int64             `json:"id"`
			GoalID      int64             `json:"goal_id"`
			Title       string            `json:"title"`
			Description string            `json:"description"`
			Status      domain.TaskStatus `json:"status"`
			ClaimedBy   int64             `json:"claimed_by"`
			Order       int               `json:"order"`
		}
		type goalListResponse struct {
			ID                int64              `json:"id"`
			DerivedFromGoalID int64              `json:"derived_from_goal_id,omitempty"`
			Title             string             `json:"title"`
			ContentChars      int                `json:"content_chars"`
			TaskCounts        goalListTaskCounts `json:"task_counts"`
			Status            domain.GoalStatus  `json:"status"`
			ClaimedBy         int64              `json:"claimed_by,omitempty"`
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
					Description: summaryLine(task.Description),
					Status:      task.Status,
					ClaimedBy:   taskHandoffClaimedBy(taskHandoffs[task.ID]),
					Order:       task.Order,
				})
			}
			visibleGoals = append(visibleGoals, goalListResponse{
				ID:                goal.ID,
				DerivedFromGoalID: goal.DerivedFromGoalID,
				Title:             summaryLine(goal.Content),
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
		mine, err := d.store.PollDecisions(ctx, p.AgentSessionID, 0)
		if err != nil {
			return nil, err
		}
		orphaned, err := d.unappliedDecisionsForSessionInProject(ctx, ns.ID, p.AgentSessionID)
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
			GoalID int64 `json:"goal_id"`
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

	case "goal.sessions":
		var p struct {
			GoalID int64 `json:"goal_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		sessions, err := d.store.ListGoalSessions(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		return marshal(map[string]any{"sessions": sessions}, nil)

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
			GoalID                  int64 `json:"goal_id"`
			AgentSessionID          int64 `json:"agent_session_id"`
			IncludeUnappliedAnswers bool  `json:"include_unapplied_answers"`
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
		unapplied, err := d.unappliedDecisionsForSession(ctx, p.GoalID, p.AgentSessionID)
		if err != nil {
			return nil, err
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               claimed,
			UnappliedDecisions: unappliedDecisionNotifications(unapplied),
		}, nil)

	case "goal.release":
		var p struct {
			GoalID int64 `json:"goal_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		err := d.store.ReleaseGoal(ctx, p.GoalID)
		return marshal(nil, err)

	case "goal.update_content":
		var p struct {
			GoalID                  int64  `json:"goal_id"`
			Content                 string `json:"content"`
			AgentSessionID          int64  `json:"agent_session_id"`
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, updated, p.GoalID, p.AgentSessionID)
		return marshal(response, err)

	case "task.update_content":
		var p struct {
			TaskID                  int64     `json:"task_id"`
			Title                   *string   `json:"title"`
			Description             *string   `json:"description"`
			Files                   *[]string `json:"files"`
			AgentSessionID          int64     `json:"agent_session_id"`
			IncludeUnappliedAnswers bool      `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goalID, err := d.store.GetTaskGoalID(ctx, p.TaskID)
		if err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, goalID)
		if err != nil {
			return nil, err
		}
		if err := d.ensureAgentSessionProject(ctx, p.AgentSessionID, goal.ProjectID); err != nil {
			return nil, err
		}
		updated, err := d.store.UpdateTaskContent(ctx, p.TaskID, p.Title, p.Description, p.Files)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(updated, err)
		}
		response, err := d.responseWithScopedUnappliedDecisions(ctx, updated, goalID, p.AgentSessionID)
		return marshal(response, err)

	case "task.declare":
		var p struct {
			GoalID                  int64      `json:"goal_id"`
			Agent                   string     `json:"agent"`
			IdempotencyKey          string     `json:"idempotency_key"`
			Titles                  []string   `json:"titles"`
			Descriptions            []string   `json:"descriptions"`
			Files                   [][]string `json:"files"`
			AgentSessionID          int64      `json:"agent_session_id"`
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, tasks, p.GoalID, p.AgentSessionID)
		return marshal(response, err)

	case "task.update":
		var p struct {
			TaskID                  int64    `json:"task_id"`
			Status                  string   `json:"status"`
			Commits                 []string `json:"commits"`
			AgentSessionID          int64    `json:"agent_session_id"`
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
		if err == nil {
			tk.Description = summaryLine(tk.Description)
		}
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(tk, err)
		}
		response, err := d.responseWithScopedUnappliedDecisions(ctx, tk, tk.GoalID, p.AgentSessionID)
		return marshal(response, err)

	case "task.claim":
		var p struct {
			TaskID                  int64 `json:"task_id"`
			AgentSessionID          int64 `json:"agent_session_id"`
			IncludeUnappliedAnswers bool  `json:"include_unapplied_answers"`
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
		unapplied, err := d.unappliedDecisionsForSession(ctx, tk.GoalID, p.AgentSessionID)
		if err != nil {
			return nil, err
		}
		return marshal(responseWithUnappliedDecisions{
			Data:               tk,
			UnappliedDecisions: unappliedDecisionNotifications(unapplied),
		}, nil)

	case "task.release":
		var p struct {
			TaskID         int64 `json:"task_id"`
			AgentSessionID int64 `json:"agent_session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		tk, err := d.store.ReleaseTaskAs(ctx, p.TaskID, p.AgentSessionID)
		return marshal(tk, err)

	case "handoff.request":
		var p struct {
			HandoffID     string `json:"handoff_id"`
			TaskID        int64  `json:"task_id"`
			RequestedBy   int64  `json:"requested_by"`
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
			TaskID     int64  `json:"task_id"`
			ReceivedBy int64  `json:"received_by"`
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
			TaskID         int64  `json:"task_id"`
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
			TaskID int64 `json:"task_id"`
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
			GoalID        int64  `json:"goal_id"`
			RequestedBy   int64  `json:"requested_by"`
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
			GoalID     int64  `json:"goal_id"`
			ReceivedBy int64  `json:"received_by"`
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
			GoalID         int64  `json:"goal_id"`
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
			GoalID                  int64           `json:"goal_id"`
			TaskID                  int64           `json:"task_id"`
			Question                string          `json:"question"`
			Options                 []domain.Option `json:"options"`
			DefaultOption           string          `json:"default_option"`
			DefaultAfterMs          *int64          `json:"default_after_ms"`
			AgentSessionID          int64           `json:"agent_session_id"`
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
		if p.TaskID != 0 {
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
			response, err := d.responseWithScopedUnappliedDecisions(ctx, data, p.GoalID, p.AgentSessionID, dec.ID)
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
			response, err := d.responseWithScopedUnappliedDecisions(ctx, data, p.GoalID, p.AgentSessionID, dec.ID)
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, data, p.GoalID, p.AgentSessionID, dec.ID)
		return marshal(response, err)

	case "decision.poll":
		var p struct {
			AgentSessionID          int64 `json:"agent_session_id"`
			DecisionID              int64 `json:"decision_id"`
			IncludeUnappliedAnswers bool  `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		if p.DecisionID != 0 {
			role, err := d.deriveSessionRole(ctx, p.AgentSessionID)
			if err != nil {
				return nil, err
			}
			if role.Role == "subcommander" && role.GoalID != 0 {
				decision, err := d.store.GetDecision(ctx, p.DecisionID)
				if err != nil {
					return nil, err
				}
				if decision.GoalID != role.GoalID {
					return nil, fmt.Errorf("%w: decision %d belongs to goal %d, not the goal %d you hold; hand it to that goal's owner instead of polling it",
						ErrDecisionOutsideGoal, p.DecisionID, decision.GoalID, role.GoalID)
				}
			}
		}
		decs, err := d.store.PollDecisions(ctx, p.AgentSessionID, p.DecisionID)
		if err != nil || !p.IncludeUnappliedAnswers {
			return marshal(decs, err)
		}
		var goalID int64
		if len(decs) > 0 {
			goalID = decs[0].GoalID
		} else if p.DecisionID != 0 {
			decision, err := d.store.GetDecision(ctx, p.DecisionID)
			if err != nil {
				return nil, err
			}
			goalID = decision.GoalID
		}
		if goalID == 0 {
			return marshal(responseWithUnappliedDecisions{Data: decs}, nil)
		}
		excludedIDs := make([]int64, 0, len(decs))
		for _, decision := range decs {
			excludedIDs = append(excludedIDs, decision.ID)
		}
		response, err := d.responseWithScopedUnappliedDecisions(ctx, decs, goalID, p.AgentSessionID, excludedIDs...)
		return marshal(response, err)

	case "decision.withdraw":
		var p struct {
			DecisionID              int64  `json:"decision_id"`
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, data, decision.GoalID, 0, decision.ID)
		return marshal(response, err)

	case "goal.set_derived_from":
		var p struct {
			GoalID                  int64 `json:"goal_id"`
			DerivedFromGoalID       int64 `json:"derived_from_goal_id"`
			AgentSessionID          int64 `json:"agent_session_id"`
			IncludeUnappliedAnswers bool  `json:"include_unapplied_answers"`
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, goal, p.GoalID, p.AgentSessionID)
		return marshal(response, err)

	case "goal.complete":
		var p struct {
			GoalID                  int64  `json:"goal_id"`
			WorkDone                string `json:"work_done"`
			NowPossible             string `json:"now_possible"`
			HowToVerify             string `json:"how_to_verify"`
			Surprises               string `json:"surprises"`
			NeedsReview             string `json:"needs_review"`
			NextSteps               string `json:"next_steps"`
			AgentSessionID          int64  `json:"agent_session_id"`
			IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, err
		}
		goal, err := d.store.GetGoal(ctx, p.GoalID)
		if err != nil {
			return nil, err
		}
		if err := d.authorizeGoalCompletion(ctx, p.GoalID, goal.ProjectID, p.AgentSessionID); err != nil {
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
		response, err := d.responseWithScopedUnappliedDecisions(ctx, dec, p.GoalID, p.AgentSessionID)
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
