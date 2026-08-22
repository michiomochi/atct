package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

const (
	EventWakeup                           = "wakeup"
	EventKeepalive                        = "keepalive"
	EventWakeupDiscrepancy                = "wakeup.discrepancy"
	EventDetectionCompletionReportMissing = "detection.completion_report_missing"
	EventDetectionCommitsMissing          = "detection.commits_missing"
	EventDetectionUndeclaredGoal          = "detection.undeclared_goal"
	EventDetectionAllTasksDropped         = "detection.all_tasks_dropped"
	EventDetectionUnclaimedDoing          = "detection.unclaimed_doing"
)

// WakeupEvent is the visible state that caused a wakeup notification. The
// counts are part of the event so a consumer can act without a second query.
type WakeupEvent struct {
	WakeupID           string `json:"wakeup_id"`
	ProjectID          string `json:"project_id"`
	ActiveGoalCount    int    `json:"active_goal_count"`
	UnstartedTaskCount int    `json:"unstarted_task_count"`
	WaitingAnswerCount int    `json:"waiting_answer_count"`
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
	DetectionID string `json:"detection_id"`
	ProjectID   string `json:"project_id"`
	GoalID      string `json:"goal_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
}

// KeepaliveEvent lets a watch process distinguish a quiet daemon from a dead
// or disconnected stream.
type KeepaliveEvent struct {
	At time.Time `json:"at"`
}

// WakeupState is the detector result used by pending output and the daemon.
// Tasks contains the actionable task list for pending's human-readable view.
type WakeupState struct {
	ActiveGoalCount        int
	UnstartedTaskCount     int
	WaitingAnswerTaskCount int
	WorkingTaskCount       int
	UntouchedTaskCount     int
	WaitingAnswerCount     int
	Tasks                  []domain.Task
	CompletedGoals         []domain.Goal
	DroppedGoals           []domain.Goal
	UnclaimedDoingTasks    []domain.Task
	UndeclaredGoals        []domain.Goal
	CommitlessGoals        []domain.Goal
}

// DetectWakeup assembles the wakeup state used by pending output and the
// daemon. It combines goal, task, decision, commit, and claim-liveness
// conditions for active goals. ClaimLiveness is the source of truth for the
// running-claim part of the wakeup condition.
func (s *Store) DetectWakeup(ctx context.Context, projectID string) (WakeupState, error) {
	goals, err := s.ListGoals(ctx, projectID)
	if err != nil {
		return WakeupState{}, err
	}
	running, _, err := ClaimLiveness(ctx, s, projectID)
	if err != nil {
		return WakeupState{}, err
	}
	runningByGoal := make(map[string]struct{}, len(running))
	for _, task := range running {
		if task.Status == domain.TaskDone || task.Status == domain.TaskDropped {
			continue
		}
		runningByGoal[task.GoalID] = struct{}{}
	}

	state := WakeupState{}
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
		hasOpenCompletionDecision := false
		for _, decision := range openDecisions {
			if decision.Kind == domain.KindCompletion && decision.Status == domain.DecisionOpen {
				hasOpenCompletionDecision = true
				break
			}
		}
		if len(tasks) == 0 {
			state.UndeclaredGoals = append(state.UndeclaredGoals, goal)
		} else {
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
				if task.Status == domain.TaskDoing && task.ClaimedBy == "" {
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
		if len(openDecisions) > 0 {
			state.WaitingAnswerCount += len(openDecisions)
		}
		var unstarted []domain.Task
		for _, task := range tasks {
			if task.Status == domain.TaskTodo && task.ClaimedBy == "" {
				unstarted = append(unstarted, task)
			}
		}
		if len(unstarted) == 0 {
			continue
		}
		state.ActiveGoalCount++
		state.UnstartedTaskCount += len(unstarted)
		if len(openDecisions) > 0 {
			state.WaitingAnswerTaskCount += len(unstarted)
		} else if _, ok := runningByGoal[goal.ID]; ok {
			state.WorkingTaskCount += len(unstarted)
		} else {
			state.UntouchedTaskCount += len(unstarted)
			state.Tasks = append(state.Tasks, unstarted...)
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
		for _, task := range tasks {
			if task.Status == domain.TaskTodo && task.ClaimedBy == "" {
				count++
			}
		}
	}
	return count, nil
}

// CountUnstartedTasksForWakeup returns the total unstarted count from
// DetectWakeup, including tasks classified as waiting for an answer, being
// worked on, or untouched. CountUnstartedTasks intentionally keeps its
// independent simple-count definition for callers that rely on it.
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
