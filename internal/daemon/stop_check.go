package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

type stopCheckResponse struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func (d *Daemon) stopCheck(ctx context.Context, sessionKey string) (stopCheckResponse, error) {
	agentSessionID, err := d.store.AgentSessionIDByKey(ctx, sessionKey)
	if err != nil {
		return stopCheckResponse{}, fmt.Errorf("resolve session key: %w", err)
	}
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil {
		return stopCheckResponse{}, fmt.Errorf("derive session role: %w", err)
	}

	// The decision below is a function of the rows the stopCheck* functions
	// read, so they record those rows into fp for the repeat check.
	fp := &stopPrint{}
	ctx = context.WithValue(ctx, stopPrintKey{}, fp)
	var detail string
	switch role.Role {
	case "commander":
		detail, err = d.stopCheckCommander(ctx, role.ProjectID)
	case "subcommander":
		detail, err = d.stopCheckSubcommander(ctx, agentSessionID, role.GoalID)
	default:
		detail, err = d.stopCheckExecutor(ctx, agentSessionID)
	}
	if err != nil {
		return stopCheckResponse{}, err
	}
	d.stopMu.Lock()
	defer d.stopMu.Unlock()
	if detail == "" {
		delete(d.stopBlocked, agentSessionID)
		return stopCheckResponse{}, nil
	}
	reason := "ATCT work remains: " + detail
	// The first stop for a reason and state always blocks. A repeat with the
	// same reason and the same state would only wake the session for nothing.
	key := reason + "\x00" + fp.b.String()
	if d.stopBlocked == nil {
		d.stopBlocked = map[int64]string{}
	}
	if d.stopBlocked[agentSessionID] == key {
		return stopCheckResponse{}, nil
	}
	d.stopBlocked[agentSessionID] = key
	return stopCheckResponse{Decision: "block", Reason: reason}, nil
}

type stopPrintKey struct{}

// stopPrint accumulates the state a stop-check decision was made from. A nil
// *stopPrint (a stopCheck* function called without stopCheck) records nothing.
type stopPrint struct{ b strings.Builder }

func stopPrintOf(ctx context.Context) *stopPrint {
	fp, _ := ctx.Value(stopPrintKey{}).(*stopPrint)
	return fp
}

func (p *stopPrint) add(format string, args ...any) {
	if p != nil {
		fmt.Fprintf(&p.b, format+"\n", args...)
	}
}

func stamp(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixNano()
}

func (p *stopPrint) goal(g domain.Goal) { p.add("goal %d %s", g.ID, g.Status) }

func (p *stopPrint) live(scope string, live bool) { p.add("live %s %t", scope, live) }

func (p *stopPrint) goalHandoffs(hs []store.GoalHandoff) {
	for _, h := range hs {
		p.add("gh %s %d %d %d %d %d %d %d %d %d", h.ID, h.RequestedBy, h.ReceivedBy, stamp(h.RequestedAt), stamp(h.ReceivedAt),
			stamp(h.ReviewRequestedAt), stamp(h.ReviewReceivedAt), stamp(h.ReviewRejectedAt), stamp(h.CompletedReportAt), stamp(h.RecoveredAt))
	}
}

func (p *stopPrint) planHandoffs(hs []store.PlanHandoff) {
	for _, h := range hs {
		p.add("ph %s %d %d %d %d %d", h.ID, h.ReviewRejectionReceivedBy, stamp(h.ReviewRequestedAt), stamp(h.ReviewReceivedAt),
			stamp(h.ReviewRejectedAt), stamp(h.CompletedReportAt))
	}
}

func (p *stopPrint) createHandoffs(hs []store.TaskCreateHandoff) {
	for _, h := range hs {
		p.add("ch %s %d %d %d %d %d", h.ID, h.ReceivedBy, stamp(h.RequestedAt), stamp(h.ReceivedAt), stamp(h.CompletedAt), stamp(h.RecoveredAt))
	}
}

func (p *stopPrint) tasks(ts []domain.Task) {
	for _, t := range ts {
		p.add("task %d %s", t.ID, t.Status)
	}
}

func (p *stopPrint) taskHandoffs(hs []store.TaskHandoff) {
	for _, h := range hs {
		p.add("th %s %d %d %d %d %d %d %d %d %d %d", h.ID, h.RequestedBy, h.ReceivedBy, stamp(h.RequestedAt), stamp(h.ReceivedAt),
			stamp(h.ReviewRequestedAt), stamp(h.ReviewReceivedAt), stamp(h.ReviewRejectedAt), stamp(h.CompletedReportAt),
			stamp(h.RecoveredAt), stamp(h.ReviewRejectionReceivedAt))
	}
}

func (p *stopPrint) decisions(ds []domain.Decision) {
	for _, d := range ds {
		p.add("decision %d %s %t %t", d.ID, d.Status, d.AnsweredAt != nil, d.AppliedAt != nil)
	}
}

func (d *Daemon) stopCheckCommander(ctx context.Context, projectID int64) (string, error) {
	goals, err := d.store.ListGoals(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("list project goals: %w", err)
	}
	fp := stopPrintOf(ctx)
	live, err := d.store.HasLiveMonitorForScope(ctx, store.MonitorLiveScope{ProjectID: projectID, Role: "commander"})
	fp.live("commander", err == nil && live)
	for _, goal := range goals {
		if goal.Status != domain.GoalActive {
			continue
		}
		fp.goal(goal)

		goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list goal handoffs for goal %d: %w", goal.ID, err)
		}
		planHandoffs, err := d.store.ListPlanHandoffs(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list plan handoffs for goal %d: %w", goal.ID, err)
		}
		decisions, err := d.store.ListDecisionsForGoal(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list decisions for goal %d: %w", goal.ID, err)
		}
		fp.goalHandoffs(goalHandoffs)
		fp.planHandoffs(planHandoffs)
		fp.decisions(decisions)

		var latestGoalReview domain.Decision
		hasGoalReview := false
		hasOpenDecision := false
		hasApprovedGoalReview := false
		for _, decision := range decisions {
			if decision.Status == domain.DecisionOpen {
				hasOpenDecision = true
			}
			if decision.Kind == domain.KindGoalReview {
				if decision.Status == domain.DecisionApplied && decision.AnswerLabel == "approve" {
					hasApprovedGoalReview = true
				}
				latestGoalReview = decision
				hasGoalReview = true
			}
		}
		if hasApprovedGoalReview {
			return fmt.Sprintf("commander has approved goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
		}
		if hasGoalReview && latestGoalReview.Status == domain.DecisionAnswered && latestGoalReview.AnswerLabel == "reject" {
			for _, handoff := range goalHandoffs {
				if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					return fmt.Sprintf("commander must return rejected goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
				}
			}
		}
		for _, handoff := range goalHandoffs {
			if handoff.RequestedAt != nil && handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt == nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("commander has unreceived goal review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
			}
		}
		for _, handoff := range planHandoffs {
			if handoff.ReviewRequestedAt != nil && handoff.ReviewReceivedAt == nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil {
				return fmt.Sprintf("commander has unreceived plan review for active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
			}
		}
		if hasOpenDecision {
			continue
		}

		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list tasks for goal %d: %w", goal.ID, err)
		}
		fp.tasks(tasks)
		hasExecutorWork := false
		for _, task := range tasks {
			handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
			}
			fp.taskHandoffs(handoffs)
			for _, handoff := range handoffs {
				if handoff.RequestedAt != nil && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					hasExecutorWork = true
					break
				}
			}
			if hasExecutorWork {
				break
			}
		}
		if hasExecutorWork {
			continue
		}

		hasDelegatedGoalHandoff := false
		for _, handoff := range goalHandoffs {
			if handoff.RequestedAt != nil && handoff.RequestedBy != handoff.ReceivedBy && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				hasDelegatedGoalHandoff = true
				break
			}
		}
		if !hasDelegatedGoalHandoff {
			return fmt.Sprintf("commander has active goal %d: %s", goal.ID, domain.Headline(goal.Content)), nil
		}
	}
	return "", nil
}

func (d *Daemon) stopCheckSubcommander(ctx context.Context, agentSessionID, goalID int64) (string, error) {
	return d.subcommanderStopDetail(ctx, agentSessionID, goalID, nil)
}

// subcommanderStopDetail is stopCheckSubcommander with the Monitor state either
// read from the store (forceLive nil) or given by the caller, so the Monitor
// form check can ask what would block the session if its Monitor were live.
func (d *Daemon) subcommanderStopDetail(ctx context.Context, agentSessionID, goalID int64, forceLive *bool) (string, error) {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("get goal: %w", err)
	}
	if err := d.store.EnsureProjectActive(ctx, goal.ProjectID); errors.Is(err, store.ErrProjectArchived) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	goalHandoffs, err := d.store.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal handoffs: %w", err)
	}
	var live bool
	if forceLive != nil {
		live = *forceLive
	} else {
		storeLive, err := d.store.HasLiveMonitorForScope(ctx, store.MonitorLiveScope{ProjectID: goal.ProjectID, Role: "subcommander", GoalID: &goalID})
		live = err == nil && storeLive
	}
	fp := stopPrintOf(ctx)
	fp.goal(goal)
	fp.live("subcommander", live)
	fp.goalHandoffs(goalHandoffs)
	planHandoffs, err := d.store.ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list plan handoffs: %w", err)
	}
	fp.planHandoffs(planHandoffs)
	decisions, err := d.store.ListDecisionsForGoal(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list decisions: %w", err)
	}
	fp.decisions(decisions)
	for _, handoff := range planHandoffs {
		if handoff.ReviewRejectionReceivedBy == agentSessionID && handoff.CompletedReportAt == nil {
			return fmt.Sprintf("subcommander has rejected plan handoff %s", handoff.ID), nil
		}
	}
	createHandoffs, err := d.store.ListTaskCreateHandoffs(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list task-create handoffs: %w", err)
	}
	fp.createHandoffs(createHandoffs)
	for _, handoff := range createHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.CompletedAt == nil && handoff.RecoveredAt == nil {
			return fmt.Sprintf("subcommander has open task-create handoff %s", handoff.ID), nil
		}
	}
	tasks, err := d.store.ListTasks(ctx, goalID)
	if err != nil {
		return "", fmt.Errorf("list goal tasks: %w", err)
	}
	fp.tasks(tasks)
	for _, task := range tasks {
		handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
		if err != nil {
			return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
		}
		fp.taskHandoffs(handoffs)
		for _, handoff := range handoffs {
			// With a live Monitor, an unreceived handoff is an executor still
			// starting; one that never starts is reported by the liveness prompt.
			if !live && handoff.RequestedAt != nil && handoff.ReceivedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("subcommander has unreceived task handoff %s for task %d", handoff.ID, task.ID), nil
			}
			if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
				return fmt.Sprintf("subcommander has task review handoff %s for task %d", handoff.ID, task.ID), nil
			}
		}
	}
	// Checked last so that the more specific reasons above are reported first.
	waiting := d.subcommanderWaiting(ctx, agentSessionID, goal, goalHandoffs, live)
	// Without a Monitor, waiting is never true, so say what the wait needs: the
	// same wait with a live Monitor would have been allowed to stop.
	waitingWithWatch := !live && d.subcommanderWaiting(ctx, agentSessionID, goal, goalHandoffs, true)
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			if waiting {
				continue
			}
			if waitingWithWatch {
				return fmt.Sprintf("subcommander has open goal handoff %s: waiting for an answer but this session has no live watch; start `atct watch --monitor --token <monitor_token> --once` in a Bash with run_in_background, then stop", handoff.ID), nil
			}
			return fmt.Sprintf("subcommander has open goal handoff %s", handoff.ID), nil
		}
	}
	return "", nil
}

// subcommanderWaiting reports whether the only thing a subcommander holds is
// an answer it is waiting for: a live Monitor on its goal scope, and either a
// plan review or a goal review the commander has not answered, or a task an
// executor is starting or working on. The answer arrives through the Monitor,
// so stopping costs nothing. When in doubt (no Monitor, a lookup error) it
// returns false and the caller keeps blocking.
func (d *Daemon) subcommanderWaiting(ctx context.Context, agentSessionID int64, goal domain.Goal, goalHandoffs []store.GoalHandoff, live bool) bool {
	if !live {
		return false
	}
	goalID := goal.ID
	for _, handoff := range goalHandoffs {
		if handoff.ReceivedBy == agentSessionID && handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
			return true
		}
	}
	planHandoffs, err := d.store.ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return false
	}
	for _, handoff := range planHandoffs {
		if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil && handoff.CompletedReportAt == nil {
			return true
		}
	}
	tasks, err := d.store.ListTasks(ctx, goalID)
	if err != nil {
		return false
	}
	for _, task := range tasks {
		handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
		if err != nil {
			return false
		}
		for _, handoff := range handoffs {
			if handoff.RequestedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil &&
				(handoff.ReviewRequestedAt == nil || handoff.ReviewRejectedAt != nil) {
				return true
			}
		}
	}
	return false
}

func (d *Daemon) stopCheckExecutor(ctx context.Context, agentSessionID int64) (string, error) {
	goals, err := d.store.ListAllGoals(ctx)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	fp := stopPrintOf(ctx)
	for _, goal := range goals {
		tasks, err := d.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return "", fmt.Errorf("list goal tasks: %w", err)
		}
		goalPrinted := false
		for _, task := range tasks {
			handoffs, err := d.store.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return "", fmt.Errorf("list task handoffs for task %d: %w", task.ID, err)
			}
			for _, handoff := range handoffs {
				// Only this session's own handoffs, and their goal, can change its decision.
				if handoff.ReceivedBy == agentSessionID && !goalPrinted {
					goalPrinted = true
					decisions, err := d.store.ListDecisionsForGoal(ctx, goal.ID)
					if err != nil {
						return "", fmt.Errorf("list decisions for goal %d: %w", goal.ID, err)
					}
					fp.goal(goal)
					fp.tasks(tasks)
					fp.decisions(decisions)
				}
				if handoff.ReceivedBy == agentSessionID {
					fp.taskHandoffs([]store.TaskHandoff{handoff})
				}
				if handoff.ReceivedBy == agentSessionID && handoff.ReceivedAt != nil && handoff.CompletedReportAt == nil && handoff.RecoveredAt == nil {
					// Review requested and not rejected: the subcommander's answer
					// arrives through the Monitor, so a live one of this task's means waiting only.
					if handoff.ReviewRequestedAt != nil && handoff.ReviewRejectedAt == nil {
						live, err := d.store.HasLiveExecutorMonitorForTask(ctx, goal.ProjectID, goal.ID, task.ID)
						fp.live(fmt.Sprintf("executor %d", task.ID), err == nil && live)
						if err == nil && live {
							continue
						}
					}
					return fmt.Sprintf("executor has open task handoff %s for task %d", handoff.ID, task.ID), nil
				}
			}
		}
	}
	return "", nil
}
