package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

const (
	EventWakeup            = "wakeup"
	EventKeepalive         = "keepalive"
	EventWakeupDiscrepancy = "wakeup.discrepancy"
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

// WakeupDiscrepancyEvent records a disagreement between the liveness-aware
// detector and an intentionally separate simple count.
type WakeupDiscrepancyEvent struct {
	WakeupID                   string `json:"wakeup_id"`
	ProjectID                  string `json:"project_id"`
	DetectorUnstartedTaskCount int    `json:"detector_unstarted_task_count"`
	CountedUnstartedTaskCount  int    `json:"counted_unstarted_task_count"`
}

// KeepaliveEvent lets a watch process distinguish a quiet daemon from a dead
// or disconnected stream.
type KeepaliveEvent struct {
	At time.Time `json:"at"`
}

// WakeupState is the detector result used by pending output and the daemon.
// Tasks contains the actionable task list for pending's human-readable view.
type WakeupState struct {
	ActiveGoalCount    int
	UnstartedTaskCount int
	WaitingAnswerCount int
	Tasks              []domain.Task
}

// DetectWakeup implements condition 5: an active goal has at least one todo
// task that has not been claimed and no running claim for that goal. Goals
// waiting on an open human decision are excluded. ClaimLiveness is the source
// of truth for the running-claim part of this condition.
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
		openDecisions, err := s.ListOpenDecisions(ctx, goal.ID)
		if err != nil {
			return WakeupState{}, err
		}
		if len(openDecisions) > 0 {
			state.WaitingAnswerCount += len(openDecisions)
			continue
		}
		if _, ok := runningByGoal[goal.ID]; ok {
			continue
		}
		tasks, err := s.ListTasks(ctx, goal.ID)
		if err != nil {
			return WakeupState{}, err
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
		state.Tasks = append(state.Tasks, unstarted...)
	}
	if state.Tasks == nil {
		state.Tasks = []domain.Task{}
	}
	return state, nil
}

// CountUnstartedTasks is deliberately an independent simple count used to
// detect drift in the liveness-aware detector. It does not consult claim
// liveness.
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

func NewWakeupID() string {
	return uuid.NewString()
}
