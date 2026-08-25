package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

const (
	EventWakeup                             = "wakeup"
	EventKeepalive                          = "keepalive"
	EventWakeupDiscrepancy                  = "wakeup.discrepancy"
	EventHandoffReported                    = "handoff_reported"
	EventDetectionCompletionReportMissing   = "detection.completion_report_missing"
	EventDetectionCommitsMissing            = "detection.commits_missing"
	EventDetectionUndeclaredGoal            = "detection.undeclared_goal"
	EventDetectionAllTasksDropped           = "detection.all_tasks_dropped"
	EventDetectionUnclaimedDoing            = "detection.unclaimed_doing"
	EventDetectionHandoffUnreceived         = "detection.handoff_unreceived"
	EventDetectionHandoffUnreported         = "detection.handoff_unreported"
	EventDetectionClaimUndelegated          = "detection.claim_undelegated"
	EventDetectionDecisionAnsweredUnapplied = "detection.decision_answered_unapplied"
	EventDetectionDecisionDefaultUnapplied  = "detection.decision_default_unapplied"
	EventDetectionClaimStale                = "detection.claim_stale"
)

// WakeupEvent is the visible state that caused a wakeup notification. The
// counts are part of the event so a consumer can act without a second query.
type WakeupEvent struct {
	WakeupID               string `json:"wakeup_id"`
	ProjectID              string `json:"project_id"`
	ActionableGoalCount    int    `json:"actionable_goal_count"`
	UnstartedTaskCount     int    `json:"unstarted_task_count"`
	WaitingAnswerTaskCount int    `json:"waiting_answer_task_count"`
	UntouchedTaskCount     int    `json:"untouched_task_count"`
	WaitingAnswerCount     int    `json:"waiting_answer_count"`
}

// WakeupDiscrepancyEvent records a disagreement between the detector and an
// independent evaluation using the same liveness-aware wakeup rules.
type WakeupDiscrepancyEvent struct {
	WakeupID                   string `json:"wakeup_id"`
	ProjectID                  string `json:"project_id"`
	DetectorUnstartedTaskCount int    `json:"detector_unstarted_task_count"`
	CountedUnstartedTaskCount  int    `json:"counted_unstarted_task_count"`
}

// DetectionEvent identifies the project and object that need attention for a
// condition-specific detection.
type DetectionEvent struct {
	DetectionID    string `json:"detection_id"`
	DecisionID     string `json:"decision_id,omitempty"`
	ProjectID      string `json:"project_id"`
	GoalID         string `json:"goal_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	HandoffID      string `json:"handoff_id,omitempty"`
	CompleteReport string `json:"complete_report,omitempty"`
}

// ReportedHandoff is a completed task or goal handoff that the daemon has not
// yet announced during this process lifetime.
type ReportedHandoff struct {
	ID             string
	GoalID         string
	TaskID         string
	CompleteReport string
}

// KeepaliveEvent lets a watch process distinguish a quiet daemon from a dead
// or disconnected stream.
type KeepaliveEvent struct {
	At time.Time `json:"at"`
}

// WakeupState is the detector result used by pending output and the daemon.
// Tasks contains the actionable task list for pending's human-readable view.
type WakeupState struct {
	ActionableGoalCount    int
	UnstartedTaskCount     int
	WaitingAnswerTaskCount int
	// WorkingTaskCount is retained for compatibility with wakeup consumers.
	// It is always zero: a claimed task is not unstarted, and an unstarted task
	// remains claimable regardless of claims on sibling tasks.
	WorkingTaskCount           int
	UntouchedTaskCount         int
	WaitingAnswerCount         int
	Tasks                      []domain.Task
	CompletedGoals             []domain.Goal
	DroppedGoals               []domain.Goal
	UnclaimedDoingTasks        []domain.Task
	UndeclaredGoals            []domain.Goal
	CommitlessGoals            []domain.Goal
	HandoffsAwaitingReceipt    []TaskHandoff
	HandoffsAwaitingReport     []TaskHandoff
	HandoffsReported           []ReportedHandoff
	UndelegatedClaims          []domain.Task
	AnsweredUnappliedDecisions []domain.Decision
	DefaultUnappliedDecisions  []domain.Decision
	StaleClaims                []domain.Task
}

func classifyWakeupDecisions(decisions []domain.Decision, projectGoalIDs map[string]struct{}) (humanAnswered, defaultApplied []domain.Decision) {
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		if _, ok := projectGoalIDs[decision.GoalID]; !ok {
			continue
		}
		if decision.DefaultAppliedAt != nil {
			defaultApplied = append(defaultApplied, decision)
			continue
		}
		humanAnswered = append(humanAnswered, decision)
	}
	return humanAnswered, defaultApplied
}

// DetectWakeup assembles the wakeup state used by pending output and the
// daemon. It combines goal, task, decision, and commit conditions for active
// goals. Unstarted tasks are classified independently so a claim on a sibling
// task does not hide work that can still be claimed.
func (s *Store) DetectWakeup(ctx context.Context, projectID string) (WakeupState, error) {
	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return WakeupState{}, err
	}
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return WakeupState{}, err
	}

	decisions, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		return WakeupState{}, err
	}
	projectGoalIDs := make(map[string]struct{}, len(goals))
	activeGoalIDs := make(map[string]struct{}, len(goals))
	for _, goal := range goals {
		projectGoalIDs[goal.ID] = struct{}{}
		if goal.Status == domain.GoalActive {
			activeGoalIDs[goal.ID] = struct{}{}
		}
	}

	state := WakeupState{}
	state.AnsweredUnappliedDecisions, state.DefaultUnappliedDecisions = classifyWakeupDecisions(decisions, projectGoalIDs)
	_, staleClaimedTasks, err := ClaimLiveness(ctx, s, projectID)
	if err != nil {
		return WakeupState{}, err
	}
	for _, task := range staleClaimedTasks {
		if _, ok := activeGoalIDs[task.GoalID]; ok {
			state.StaleClaims = append(state.StaleClaims, task)
		}
	}

	now := time.Now().UTC()
	for _, goal := range goals {
		if goal.Status != domain.GoalActive {
			continue
		}
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return WakeupState{}, err
		}
		openDecisions, err := s.ListOpenDecisions(ctx, goal.ID)
		if err != nil {
			return WakeupState{}, err
		}
		goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return WakeupState{}, err
		}
		for _, handoff := range goalHandoffs {
			if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt != nil {
				state.HandoffsReported = append(state.HandoffsReported, ReportedHandoff{
					ID:             handoff.ID,
					GoalID:         handoff.GoalID,
					CompleteReport: handoff.CompleteReport,
				})
			}
		}
		hasOpenCompletionDecision := false
		for _, decision := range openDecisions {
			if decision.Kind == domain.KindCompletion && decision.Status == domain.DecisionOpen {
				hasOpenCompletionDecision = true
				break
			}
		}
		taskHandoffs := make(map[string][]TaskHandoff, len(tasks))
		taskClaimed := make(map[string]bool, len(tasks))
		if len(tasks) == 0 {
			state.UndeclaredGoals = append(state.UndeclaredGoals, goal)
		} else {
			for _, task := range tasks {
				handoffs, err := s.ListTaskHandoffs(ctx, task.ID)
				if err != nil {
					return WakeupState{}, err
				}
				taskHandoffs[task.ID] = handoffs
				for _, handoff := range handoffs {
					if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt != nil {
						state.HandoffsReported = append(state.HandoffsReported, ReportedHandoff{
							ID:             handoff.ID,
							TaskID:         handoff.TaskID,
							CompleteReport: handoff.CompleteReport,
						})
					}
					if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
						taskClaimed[task.ID] = true
						break
					}
				}
			}
			allDone := true
			allDropped := true
			allTerminal := true
			hasDoneTask := false
			for _, task := range tasks {
				if task.Status != domain.TaskDone {
					allDone = false
				} else {
					hasDoneTask = true
				}
				if task.Status != domain.TaskDropped {
					allDropped = false
				}
				if task.Status != domain.TaskDone && task.Status != domain.TaskDropped {
					allTerminal = false
				}
				if task.Status == domain.TaskDoing && !taskClaimed[task.ID] {
					state.UnclaimedDoingTasks = append(state.UnclaimedDoingTasks, task)
				}
			}
			if allDone && !hasOpenCompletionDecision {
				state.CompletedGoals = append(state.CompletedGoals, goal)
			} else if allDropped && !hasOpenCompletionDecision {
				state.DroppedGoals = append(state.DroppedGoals, goal)
			}
			if allTerminal && hasDoneTask && !hasOpenCompletionDecision {
				hasLinkedCommit := false
				for _, task := range tasks {
					commits, err := s.ListTaskCommits(ctx, task.ID)
					if err != nil {
						return WakeupState{}, err
					}
					if len(commits) > 0 {
						hasLinkedCommit = true
						break
					}
				}
				if !hasLinkedCommit {
					state.CommitlessGoals = append(state.CommitlessGoals, goal)
				}
			}
		}
		for _, task := range tasks {
			handoffs := taskHandoffs[task.ID]
			delegated := false
			for _, handoff := range handoffs {
				isDelegated := handoff.RequestedAt != nil &&
					(handoff.ReceivedAt == nil || strings.TrimSpace(handoff.RequestedBy) != strings.TrimSpace(handoff.ReceivedBy))
				if isDelegated {
					delegated = true
					if handoff.ReceivedAt == nil {
						state.HandoffsAwaitingReceipt = append(state.HandoffsAwaitingReceipt, handoff)
					}
				}
				if isDelegated && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
					state.HandoffsAwaitingReport = append(state.HandoffsAwaitingReport, handoff)
				}
			}
			if taskClaimed[task.ID] && !delegated {
				commanderClaim := false
				sessionID := ""
				for _, handoff := range handoffs {
					if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
						sessionID = strings.TrimSpace(handoff.ReceivedBy)
						break
					}
				}
				if sessionID != "" {
					for _, project := range projects {
						if strings.TrimSpace(project.ClaimedBy) == sessionID {
							commanderClaim = true
							break
						}
					}
				}
				if !commanderClaim {
					state.UndelegatedClaims = append(state.UndelegatedClaims, task)
				}
			}
		}
		if len(openDecisions) > 0 {
			state.WaitingAnswerCount += len(openDecisions)
		}
		var unstarted []domain.Task
		for _, task := range tasks {
			if task.Status == domain.TaskTodo && !taskClaimed[task.ID] && (task.SnoozedUntil == nil || !task.SnoozedUntil.After(now)) {
				unstarted = append(unstarted, task)
			}
		}
		if len(unstarted) == 0 {
			continue
		}
		state.ActionableGoalCount++
		state.UnstartedTaskCount += len(unstarted)
		openDecisionTaskIDs := make(map[string]struct{}, len(openDecisions))
		for _, decision := range openDecisions {
			if decision.TaskID != "" {
				openDecisionTaskIDs[decision.TaskID] = struct{}{}
			}
		}
		for _, task := range unstarted {
			if _, ok := openDecisionTaskIDs[task.ID]; ok {
				state.WaitingAnswerTaskCount++
				continue
			}
			state.UntouchedTaskCount++
			state.Tasks = append(state.Tasks, task)
		}
	}
	if state.Tasks == nil {
		state.Tasks = []domain.Task{}
	}
	if state.CompletedGoals == nil {
		state.CompletedGoals = []domain.Goal{}
	}
	if state.DroppedGoals == nil {
		state.DroppedGoals = []domain.Goal{}
	}
	if state.UnclaimedDoingTasks == nil {
		state.UnclaimedDoingTasks = []domain.Task{}
	}
	if state.UndeclaredGoals == nil {
		state.UndeclaredGoals = []domain.Goal{}
	}
	if state.CommitlessGoals == nil {
		state.CommitlessGoals = []domain.Goal{}
	}
	if state.HandoffsAwaitingReceipt == nil {
		state.HandoffsAwaitingReceipt = []TaskHandoff{}
	}
	if state.HandoffsAwaitingReport == nil {
		state.HandoffsAwaitingReport = []TaskHandoff{}
	}
	if state.HandoffsReported == nil {
		state.HandoffsReported = []ReportedHandoff{}
	}
	if state.UndelegatedClaims == nil {
		state.UndelegatedClaims = []domain.Task{}
	}
	if state.AnsweredUnappliedDecisions == nil {
		state.AnsweredUnappliedDecisions = []domain.Decision{}
	}
	if state.DefaultUnappliedDecisions == nil {
		state.DefaultUnappliedDecisions = []domain.Decision{}
	}
	if state.StaleClaims == nil {
		state.StaleClaims = []domain.Task{}
	}
	return state, nil
}

// CountUnstartedTasks returns an independent simple count that does not
// consult claim liveness. Callers that need the wakeup definition should use
// CountUnstartedTasksForWakeup instead.
func (s *Store) CountUnstartedTasks(ctx context.Context, projectID string) (int, error) {
	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return 0, err
	}
	count := 0
	now := time.Now().UTC()
	for _, goal := range goals {
		if goal.Status != domain.GoalActive {
			continue
		}
		openDecisions, err := s.ListOpenDecisions(ctx, goal.ID)
		if err != nil {
			return 0, err
		}
		if len(openDecisions) > 0 {
			continue
		}
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return 0, err
		}
		taskClaimed := make(map[string]bool, len(tasks))
		for _, task := range tasks {
			handoffs, err := s.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return 0, err
			}
			for _, handoff := range handoffs {
				if handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil {
					taskClaimed[task.ID] = true
					break
				}
			}
		}
		for _, task := range tasks {
			if task.Status == domain.TaskTodo && !taskClaimed[task.ID] && (task.SnoozedUntil == nil || !task.SnoozedUntil.After(now)) {
				count++
			}
		}
	}
	return count, nil
}

// CountUnstartedTasksForWakeup returns the total unstarted count from
// DetectWakeup, including tasks classified as waiting for an answer or
// untouched. CountUnstartedTasks intentionally keeps its independent
// simple-count definition for callers that rely on it.
func (s *Store) CountUnstartedTasksForWakeup(ctx context.Context, projectID string) (int, error) {
	state, err := s.DetectWakeup(ctx, projectID)
	if err != nil {
		return 0, err
	}
	return state.UnstartedTaskCount, nil
}

func NewWakeupID() string {
	return uuid.NewString()
}

func NewDetectionID() string {
	return uuid.NewString()
}
