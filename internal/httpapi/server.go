package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type Server struct {
	store *store.Store
}

func New(s *store.Store) *Server {
	return &Server{store: s}
}

func (s *Server) Handler() http.Handler {
	return s
}

type TaskView struct {
	domain.Task
	HeldForSeconds int64             `json:"held_for_seconds"`
	OpenDecisions  []domain.Decision `json:"open_decisions"`
	ProjectID      string            `json:"project_id"`
	ProjectName    string            `json:"project_name"`
}

type goalView struct {
	domain.Goal
	AwaitingDecision bool       `json:"awaiting_decision"`
	ProjectName      string     `json:"project_name"`
	Tasks            []TaskView `json:"tasks"`
}

type decisionView struct {
	domain.Decision
	ProjectID        string `json:"project_id"`
	ProjectName      string `json:"project_name"`
	GoalTitle        string `json:"goal_title"`
	DefaultOption    string `json:"default_option"`
	DefaultAfterMs   *int64 `json:"default_after_ms,omitempty"`
	SettledByDefault bool   `json:"settled_by_default"`
}

type inboxResponse struct {
	OpenDecisions      []decisionView `json:"open_decisions"`
	UnappliedDecisions []decisionView `json:"unapplied_decisions"`
	ActiveGoals        []goalView     `json:"active_goals"`
	AttentionTasks     []TaskView     `json:"attention_tasks"`
}

type goalResponse struct {
	Goal                   goalView              `json:"goal"`
	Now                    []TaskView            `json:"now"`
	NeedsDecision          []TaskView            `json:"needs_decision"`
	UnattachedDecisions    []domain.Decision     `json:"unattached_decisions"`
	Next                   []TaskView            `json:"next"`
	DecisionHistory        []decisionHistoryView `json:"decision_history"`
	DecisionHistoryOmitted int                   `json:"decision_history_omitted"`
}

type decisionHistoryView struct {
	DecisionID       string     `json:"decision_id"`
	TaskID           string     `json:"task_id"`
	Question         string     `json:"question"`
	AnswerLabel      string     `json:"answer_label"`
	AnswerText       string     `json:"answer_text"`
	SettledByDefault bool       `json:"settled_by_default"`
	DefaultAppliedAt *time.Time `json:"default_applied_at"`
	AnsweredAt       *time.Time `json:"answered_at"`
	AppliedAt        *time.Time `json:"applied_at"`
}

type answerRequest struct {
	AnswerLabel string `json:"answer_label"`
	AnswerText  string `json:"answer_text"`
}

type reviseDecisionRequest struct {
	Options []domain.Option `json:"options"`
}

type rejectionRequest struct {
	Reason string `json:"reason"`
}

type createGoalRequest struct {
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "inbox" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleInbox(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "events" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleEvents(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "projects" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleProjects(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "goals" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleCreateGoal(w, r)
		return
	}

	if len(parts) == 3 && parts[0] == "api" && parts[1] == "goals" {
		if parts[2] == "" {
			writeError(w, http.StatusBadRequest, "goal id is missing")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleGoal(w, r, parts[2])
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "tasks" && parts[3] == "release" {
		if parts[2] == "" {
			writeError(w, http.StatusBadRequest, "task id is missing")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleRelease(w, r, parts[2])
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "decisions" {
		if parts[2] == "" || parts[3] == "" {
			writeError(w, http.StatusBadRequest, "decision path is malformed")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusBadRequest, "method is not allowed for this endpoint")
			return
		}
		s.handleDecision(w, r, parts[2], parts[3])
		return
	}

	if malformedAPIPath(r.URL.Path) {
		writeError(w, http.StatusBadRequest, "api path is malformed")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	writeError(w, http.StatusNotFound, "endpoint not found")
}

func malformedAPIPath(path string) bool {
	for _, prefix := range []string{"/api/inbox", "/api/events", "/api/projects", "/api/goals", "/api/tasks", "/api/decisions"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if projects == nil {
		projects = []domain.Project{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (s *Server) handleCreateGoal(w http.ResponseWriter, r *http.Request) {
	var request createGoalRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	request.ProjectID = strings.TrimSpace(request.ProjectID)
	if request.ProjectID == "" {
		writeError(w, http.StatusBadRequest, "project_id is required")
		return
	}
	if strings.TrimSpace(request.Title) == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	projects, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	knownProject := false
	for _, project := range projects {
		if project.ID == request.ProjectID {
			knownProject = true
			break
		}
	}
	if !knownProject {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	goal, err := s.store.CreateGoal(r.Context(), request.ProjectID, request.Title, request.Description)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, goal)
}

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	goals, err := s.store.ListAllGoals(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	openDecisions, err := s.store.ListAllOpenDecisions(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	unapplied, err := s.store.ListUnappliedDecisions(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projectNames := make(map[string]string, len(projects))
	for _, project := range projects {
		projectNames[project.ID] = project.Name
	}
	goalProjectIDs := make(map[string]string, len(goals))
	goalProjectNames := make(map[string]string, len(goals))
	goalTitles := make(map[string]string, len(goals))
	for _, goal := range goals {
		goalProjectIDs[goal.ID] = goal.ProjectID
		goalProjectNames[goal.ID] = projectNames[goal.ProjectID]
		goalTitles[goal.ID] = goal.Title
	}

	openByTask := indexDecisions(openDecisions)
	openByGoal := make(map[string]bool, len(openDecisions))
	for _, decision := range openDecisions {
		openByGoal[decision.GoalID] = true
	}
	openDecisionViews := make([]decisionView, 0, len(openDecisions))
	for _, decision := range openDecisions {
		openDecisionViews = append(openDecisionViews, decisionView{
			Decision:         decision,
			ProjectID:        goalProjectIDs[decision.GoalID],
			ProjectName:      goalProjectNames[decision.GoalID],
			GoalTitle:        goalTitles[decision.GoalID],
			DefaultOption:    decision.DefaultOption,
			DefaultAfterMs:   decision.DefaultAfterMs,
			SettledByDefault: decision.DefaultAppliedAt != nil,
		})
	}
	unappliedDecisionViews := make([]decisionView, 0, len(unapplied))
	for _, decision := range unapplied {
		unappliedDecisionViews = append(unappliedDecisionViews, decisionView{
			Decision:         decision,
			ProjectID:        goalProjectIDs[decision.GoalID],
			ProjectName:      goalProjectNames[decision.GoalID],
			GoalTitle:        goalTitles[decision.GoalID],
			DefaultOption:    decision.DefaultOption,
			DefaultAfterMs:   decision.DefaultAfterMs,
			SettledByDefault: decision.DefaultAppliedAt != nil,
		})
	}
	activeGoals := make([]goalView, 0)
	attentionTasks := make([]TaskView, 0)
	for _, goal := range goals {
		tasks, err := s.store.ListTasks(ctx, goal.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if goal.Status == domain.GoalActive {
			goalTasks := append([]domain.Task(nil), tasks...)
			for i := 1; i < len(goalTasks); i++ {
				for j := i; j > 0 && goalTasks[j].Order < goalTasks[j-1].Order; j-- {
					goalTasks[j], goalTasks[j-1] = goalTasks[j-1], goalTasks[j]
				}
			}
			taskViews := make([]TaskView, 0, len(goalTasks))
			for _, task := range goalTasks {
				taskViews = append(taskViews, newTaskView(task, openByTask[task.ID]))
			}
			activeGoals = append(activeGoals, goalView{
				Goal:             goal,
				AwaitingDecision: openByGoal[goal.ID],
				ProjectName:      projectNames[goal.ProjectID],
				Tasks:            taskViews,
			})
		}
		for _, task := range tasks {
			decisions := openByTask[task.ID]
			if len(decisions) == 0 || !isProjectableTask(task.Status) {
				continue
			}
			taskView := newTaskView(task, decisions)
			taskView.ProjectID = goal.ProjectID
			taskView.ProjectName = projectNames[goal.ProjectID]
			attentionTasks = append(attentionTasks, taskView)
		}
	}

	writeJSON(w, http.StatusOK, inboxResponse{
		OpenDecisions:      openDecisionViews,
		UnappliedDecisions: unappliedDecisionViews,
		ActiveGoals:        activeGoals,
		AttentionTasks:     nonNilTaskViews(attentionTasks),
	})
}

func (s *Server) handleGoal(w http.ResponseWriter, r *http.Request, goalID string) {
	ctx := r.Context()
	goal, err := s.store.GetGoal(ctx, goalID)
	if errors.Is(err, store.ErrGoalNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projectName := ""
	for _, project := range projects {
		if project.ID == goal.ProjectID {
			projectName = project.Name
			break
		}
	}
	tasks, err := s.store.ListTasks(ctx, goalID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	openDecisions, err := s.store.ListOpenDecisions(ctx, goalID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	appliedDecisions, decisionHistoryOmitted, err := s.store.ListAppliedDecisions(ctx, goalID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	decisionHistory := make([]decisionHistoryView, 0, len(appliedDecisions))
	for _, decision := range appliedDecisions {
		decisionHistory = append(decisionHistory, decisionHistoryView{
			DecisionID:       decision.ID,
			TaskID:           decision.TaskID,
			Question:         decision.Question,
			AnswerLabel:      decision.AnswerLabel,
			AnswerText:       decision.AnswerText,
			SettledByDefault: decision.DefaultAppliedAt != nil,
			DefaultAppliedAt: decision.DefaultAppliedAt,
			AnsweredAt:       decision.AnsweredAt,
			AppliedAt:        decision.AppliedAt,
		})
	}
	openByTask := indexDecisions(openDecisions)
	unattachedDecisions := make([]domain.Decision, 0)
	for _, decision := range openDecisions {
		if decision.TaskID == "" {
			unattachedDecisions = append(unattachedDecisions, decision)
		}
	}

	allTaskViews := make([]TaskView, 0, len(tasks))
	response := goalResponse{
		Goal:                   goalView{Goal: goal, ProjectName: projectName},
		Now:                    make([]TaskView, 0),
		NeedsDecision:          make([]TaskView, 0),
		UnattachedDecisions:    unattachedDecisions,
		Next:                   make([]TaskView, 0),
		DecisionHistory:        decisionHistory,
		DecisionHistoryOmitted: decisionHistoryOmitted,
	}
	for _, task := range tasks {
		decisions := openByTask[task.ID]
		view := newTaskView(task, decisions)
		allTaskViews = append(allTaskViews, view)
		switch {
		case len(decisions) > 0 && isProjectableTask(task.Status):
			response.NeedsDecision = append(response.NeedsDecision, view)
		case task.Status == domain.TaskStatus("doing"):
			response.Now = append(response.Now, view)
		case task.Status == domain.TaskStatus("todo"):
			response.Next = append(response.Next, view)
		}
	}
	response.Goal.Tasks = nonNilTaskViews(allTaskViews)

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request, taskID string) {
	task, err := s.store.ReleaseTask(r.Context(), taskID)
	if err != nil {
		if strings.Contains(err.Error(), "task not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request, decisionID, action string) {
	switch action {
	case "answer":
		s.handleAnswer(w, r, decisionID)
	case "revise":
		s.handleRevise(w, r, decisionID)
	case "approve":
		s.handleApprove(w, r, decisionID)
	case "reject":
		s.handleReject(w, r, decisionID)
	default:
		writeError(w, http.StatusNotFound, "endpoint not found")
	}
}

func (s *Server) handleAnswer(w http.ResponseWriter, r *http.Request, decisionID string) {
	var request answerRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.AnswerLabel) == "" && strings.TrimSpace(request.AnswerText) == "" {
		writeError(w, http.StatusBadRequest, "an answer label or text is required")
		return
	}
	if !s.ensureOpenDecision(w, r.Context(), decisionID) {
		return
	}
	decision, err := s.store.AnswerDecision(r.Context(), store.AnswerInput{
		DecisionID:  decisionID,
		AnswerLabel: request.AnswerLabel,
		AnswerText:  request.AnswerText,
	})
	if errors.Is(err, store.ErrDecisionNotOpen) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

func (s *Server) handleRevise(w http.ResponseWriter, r *http.Request, decisionID string) {
	var request reviseDecisionRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(request.Options) == 0 {
		writeError(w, http.StatusBadRequest, "at least one option is required")
		return
	}

	original, err := s.store.GetDecision(r.Context(), decisionID)
	if errors.Is(err, store.ErrDecisionNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if original.DefaultAppliedAt == nil && original.AnsweredAt == nil {
		writeError(w, http.StatusConflict, "decision is not settled")
		return
	}

	revised, err := s.store.AskDecision(r.Context(), store.AskInput{
		GoalID:   original.GoalID,
		TaskID:   original.TaskID,
		Kind:     original.Kind,
		Question: revisionQuestion(original),
		Options:  request.Options,
		RunID:    original.RunID,
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, revised)
}

func revisionQuestion(original domain.Decision) string {
	selected := strings.TrimSpace(original.AnswerLabel)
	if selected == "" {
		selected = strings.TrimSpace(original.DefaultOption)
	}
	if answerText := strings.TrimSpace(original.AnswerText); answerText != "" {
		if selected == "" {
			selected = answerText
		} else {
			selected += " - " + answerText
		}
	}
	if selected == "" {
		selected = "(no option recorded)"
	}
	return fmt.Sprintf("Reconsider the decision %q. The selected option was %q.", original.Question, selected)
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request, decisionID string) {
	var request struct{}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	decision, ok := s.getOpenCompletion(w, r.Context(), decisionID)
	if !ok {
		return
	}
	goal, err := s.store.ApproveCompletion(r.Context(), decision.ID)
	if errors.Is(err, store.ErrDecisionNotOpen) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, goal)
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request, decisionID string) {
	var request rejectionRequest
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, ok := s.getOpenCompletion(w, r.Context(), decisionID); !ok {
		return
	}
	err := s.store.RejectCompletion(r.Context(), decisionID, request.Reason)
	if errors.Is(err, store.ErrDecisionNotOpen) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	decision, err := s.store.GetDecision(r.Context(), decisionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

func (s *Server) ensureOpenDecision(w http.ResponseWriter, ctx context.Context, decisionID string) bool {
	decision, err := s.store.GetDecision(ctx, decisionID)
	if errors.Is(err, store.ErrDecisionNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return false
	}
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if decision.Status != domain.DecisionOpen {
		writeError(w, http.StatusConflict, store.ErrDecisionNotOpen.Error())
		return false
	}
	return true
}

func (s *Server) getOpenCompletion(w http.ResponseWriter, ctx context.Context, decisionID string) (domain.Decision, bool) {
	decision, err := s.store.GetDecision(ctx, decisionID)
	if errors.Is(err, store.ErrDecisionNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return domain.Decision{}, false
	}
	if err != nil {
		writeStoreError(w, err)
		return domain.Decision{}, false
	}
	if decision.Status != domain.DecisionOpen || decision.Kind != domain.KindCompletion {
		writeError(w, http.StatusConflict, store.ErrDecisionNotOpen.Error())
		return domain.Decision{}, false
	}
	return decision, true
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	projectID := r.URL.Query().Get("project_id")
	ch, cancel := s.store.SubscribeDecisionEvents()
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			if projectID != "" {
				goal, err := s.store.GetGoal(r.Context(), event.Decision.GoalID)
				if err != nil || goal.ProjectID != projectID {
					continue
				}
			}
			data, err := json.Marshal(event.Decision)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Name, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func decodeJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return io.EOF
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func indexDecisions(decisions []domain.Decision) map[string][]domain.Decision {
	byTask := make(map[string][]domain.Decision)
	for _, decision := range decisions {
		if decision.TaskID == "" {
			continue
		}
		byTask[decision.TaskID] = append(byTask[decision.TaskID], decision)
	}
	return byTask
}

func newTaskView(task domain.Task, decisions []domain.Decision) TaskView {
	if decisions == nil {
		decisions = make([]domain.Decision, 0)
	}
	held := int64(0)
	if task.ClaimedBy != "" && task.ClaimedAt != nil {
		if elapsed := time.Since(*task.ClaimedAt); elapsed > 0 {
			held = int64(elapsed / time.Second)
		}
	}
	return TaskView{Task: task, HeldForSeconds: held, OpenDecisions: decisions}
}

func isProjectableTask(status domain.TaskStatus) bool {
	return status == domain.TaskStatus("doing") || status == domain.TaskStatus("todo")
}

func nonNilDecisions(value []domain.Decision) []domain.Decision {
	if value == nil {
		return make([]domain.Decision, 0)
	}
	return value
}

func nonNilGoals(value []domain.Goal) []domain.Goal {
	if value == nil {
		return make([]domain.Goal, 0)
	}
	return value
}

func nonNilTaskViews(value []TaskView) []TaskView {
	if value == nil {
		return make([]TaskView, 0)
	}
	return value
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeStoreError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, err.Error())
}
