package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

var errNoPendingDecisions = errors.New("no unapplied decisions")

const (
	atctAgentSessionIDEnv          = "ATCT_AGENT_SESSION_ID"
	unfinishedClaimMarker          = "Unfinished tasks with work locks:"
	staleClaimMarker               = "Stale work locks:"
	undeclaredGoalMarker           = "Undeclared active goals:"
	pendingDecisionReason          = "A human answered a decision you parked. Call `atct_decision_poll` with each decision_id below, then continue the work that was waiting on it."
	pendingDefaultDecisionReason   = "No one answered a decision you parked, so its default was applied. Call `atct_decision_poll` with each decision_id below, then continue the work that was waiting on it."
	pendingClaimReason             = "You hold"
	pendingNoDefaultDecisionReason = "You are waiting on a human for %d decisions with no default. That does not\nblock the %d tasks below."
	pendingStaleClaimReason        = "A task with a work lock held by another agent session is no longer running. You can take it over by returning it to todo with `atct_task_update`, then acquire the work lock with `atct_task_claim`."
	pendingUndeclaredGoalReason    = "An active goal has no tasks. Call `atct_task_create` for each goal below, then continue the work."
	pendingWakeupReason            = "An active goal has unstarted tasks available to claim. Call `atct_task_claim` for a task below, then continue the work."
	pendingCompletedGoalReason     = "All tasks are done but the active goal has no completion report. Call `atct_goal_complete` for each goal below, then continue the work."
	pendingCommitlessGoalReason    = "All tasks in an active goal are done but no task has a linked commit. Call `atct_task_update` with `commits` for at least one task below to link its commit, then continue the work."
	pendingDroppedGoalReason       = "All tasks in an active goal were dropped. Call `atct_goal_complete` to report that the work was withdrawn; call `atct_task_create` to create tasks again if it should be resumed."
	pendingUnclaimedDoingReason    = "A task is doing without a work lock. Return it to todo with `atct_task_update`, then call `atct_task_claim` before continuing the work."
	completedGoalMarker            = "Goals with all tasks done:"
	commitlessGoalMarker           = "Goals with no linked commits:"
	droppedGoalMarker              = "Goals with all tasks dropped:"
	unclaimedDoingMarker           = "Doing tasks without a work lock:"
)

func currentAgentSessionID() int64 {
	value := strings.TrimSpace(os.Getenv(atctAgentSessionIDEnv))
	if value == "" {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func pendingCommand(dir, cwd string) (string, int, error) {
	return pendingCommandForProject(dir, cwd, "", false)
}

func pendingCommandForProject(dir, cwd, projectName string, projectSpecified bool) (string, int, error) {
	output, err := pendingTextForProject(dir, cwd, projectName, projectSpecified)
	if err != nil {
		return "", 0, err
	}
	if output == "" {
		return "", 1, nil
	}
	return output, 0, nil
}

func pendingText(dir, cwd string) (string, error) {
	return pendingTextForProject(dir, cwd, "", false)
}

func pendingTextForProject(dir, cwd, projectName string, projectSpecified bool) (string, error) {
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if projectSpecified {
				return "", fmt.Errorf("project %q not found", projectName)
			}
			return "", nil
		}
		return "", fmt.Errorf("stat store: %w", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	project, err := resolveProjectSelection(ctx, s, cwd, projectName, projectSpecified)
	if errors.Is(err, store.ErrProjectNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}

	goals, err := s.ListGoals(ctx, project.ID)
	if err != nil {
		return "", fmt.Errorf("list goals: %w", err)
	}
	decisions, err := s.ListUnappliedDecisions(ctx)
	if err != nil {
		return "", fmt.Errorf("list unapplied decisions: %w", err)
	}
	projectGoalIDs := make(map[int64]struct{}, len(goals))
	for _, goal := range goals {
		projectGoalIDs[goal.ID] = struct{}{}
	}

	unfinishedTasks := make([]domain.Task, 0)
	agentSessionID := currentAgentSessionID()
	if agentSessionID == 0 {
		agentSessionID, err = s.LatestAgentSessionID(ctx, project.ID)
		if err != nil {
			return "", fmt.Errorf("find latest agent session: %w", err)
		}
	}
	openNoDefaultDecisionCount := 0
	if agentSessionID != 0 {
		for _, goal := range goals {
			openDecisions, err := s.ListOpenDecisions(ctx, goal.ID)
			if err != nil {
				return "", fmt.Errorf("list open decisions for goal %d: %w", goal.ID, err)
			}
			for _, decision := range openDecisions {
				if decision.Status != "open" || decision.DefaultOption != "" || decision.AgentSessionID != agentSessionID {
					continue
				}
				openNoDefaultDecisionCount++
			}
		}
	}
	if agentSessionID != 0 {
		for _, goal := range goals {
			if goal.Status != domain.GoalActive {
				continue
			}
			tasks, err := s.ListOpenTasksClaimedBy(ctx, goal.ID, agentSessionID)
			if err != nil {
				return "", fmt.Errorf("list claimed tasks for goal %d: %w", goal.ID, err)
			}
			unfinishedTasks = append(unfinishedTasks, tasks...)
		}
	}

	_, staleClaimedTasks, err := store.ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		return "", fmt.Errorf("check claim liveness: %w", err)
	}
	activeGoalIDs := make(map[int64]struct{}, len(goals))
	openTaskHandoffs := make(map[int64]*store.TaskHandoff)
	for _, goal := range goals {
		if goal.Status == domain.GoalActive {
			activeGoalIDs[goal.ID] = struct{}{}
			handoffs, err := s.ListOpenTaskHandoffsForGoal(ctx, goal.ID)
			if err != nil {
				return "", fmt.Errorf("list open task handoffs for goal %d: %w", goal.ID, err)
			}
			for taskID, handoff := range handoffs {
				openTaskHandoffs[taskID] = handoff
			}
		}
	}
	otherStaleClaimedTasks := make([]domain.Task, 0)
	for _, task := range staleClaimedTasks {
		if _, ok := activeGoalIDs[task.GoalID]; !ok {
			continue
		}
		if contextTaskHandoffOwner(openTaskHandoffs[task.ID]) == agentSessionID {
			continue
		}
		otherStaleClaimedTasks = append(otherStaleClaimedTasks, task)
	}
	wakeupState, err := s.DetectWakeup(ctx, project.ID)
	if err != nil {
		return "", fmt.Errorf("detect wakeup: %w", err)
	}

	var output strings.Builder
	humanAnsweredDecisions := make([]domain.Decision, 0)
	defaultAppliedDecisions := make([]domain.Decision, 0)
	for _, decision := range decisions {
		if decision.Status != domain.DecisionAnswered || decision.AppliedAt != nil {
			continue
		}
		if _, ok := projectGoalIDs[decision.GoalID]; !ok {
			continue
		}
		if decision.DefaultAppliedAt != nil {
			defaultAppliedDecisions = append(defaultAppliedDecisions, decision)
			continue
		}
		humanAnsweredDecisions = append(humanAnsweredDecisions, decision)
	}
	writeDecisionSection := func(reason string, section []domain.Decision) {
		if len(section) == 0 {
			return
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(reason)
		output.WriteString("\n\n")
		for _, decision := range section {
			fmt.Fprintf(&output, "- %s (decision_id: %d)\n", oneLine(decision.Question), decision.ID)
		}
	}
	writeDecisionSection(pendingDecisionReason, humanAnsweredDecisions)
	writeDecisionSection(pendingDefaultDecisionReason, defaultAppliedDecisions)
	if len(otherStaleClaimedTasks) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingStaleClaimReason)
		output.WriteString("\n\n")
		output.WriteString(staleClaimMarker)
		output.WriteByte('\n')
		for _, task := range otherStaleClaimedTasks {
			fmt.Fprintf(&output, "- %s (task_id: %d)\n", oneLine(task.Title), task.ID)
		}
	}
	if len(wakeupState.UndeclaredGoals) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingUndeclaredGoalReason)
		output.WriteString("\n\n")
		output.WriteString(undeclaredGoalMarker)
		output.WriteByte('\n')
		for _, goal := range wakeupState.UndeclaredGoals {
			fmt.Fprintf(&output, "- %s (goal_id: %d)\n", oneLine(domain.Headline(goal.Content)), goal.ID)
		}
	}
	availableTaskCount := len(wakeupState.Tasks)
	if openNoDefaultDecisionCount > 0 && availableTaskCount > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(fmt.Sprintf(pendingNoDefaultDecisionReason, openNoDefaultDecisionCount, availableTaskCount))
	}
	if len(wakeupState.Tasks) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingWakeupReason)
		output.WriteString("\n\n")
		output.WriteString("Unstarted tasks:")
		output.WriteByte('\n')
		for _, task := range wakeupState.Tasks {
			fmt.Fprintf(&output, "- %s (task_id: %d)\n", oneLine(task.Title), task.ID)
		}
	}
	if len(unfinishedTasks) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingClaimReasonFor(
			len(unfinishedTasks),
			wakeupState.UnstartedTaskCount,
			wakeupState.WaitingAnswerTaskCount,
			availableTaskCount,
		))
		output.WriteString("\n\n")
		output.WriteString(unfinishedClaimMarker)
		output.WriteByte('\n')
		for _, task := range unfinishedTasks {
			fmt.Fprintf(&output, "- %s (task_id: %d)\n", oneLine(task.Title), task.ID)
		}
	}
	if len(wakeupState.CompletedGoals) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingCompletedGoalReason)
		output.WriteString("\n\n")
		output.WriteString(completedGoalMarker)
		output.WriteByte('\n')
		for _, goal := range wakeupState.CompletedGoals {
			fmt.Fprintf(&output, "- %s (goal_id: %d)\n", oneLine(domain.Headline(goal.Content)), goal.ID)
		}
	}
	if len(wakeupState.CommitlessGoals) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingCommitlessGoalReason)
		output.WriteString("\n\n")
		output.WriteString(commitlessGoalMarker)
		output.WriteByte('\n')
		for _, goal := range wakeupState.CommitlessGoals {
			fmt.Fprintf(&output, "- %s (goal_id: %d)\n", oneLine(domain.Headline(goal.Content)), goal.ID)
		}
	}
	if len(wakeupState.DroppedGoals) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingDroppedGoalReason)
		output.WriteString("\n\n")
		output.WriteString(droppedGoalMarker)
		output.WriteByte('\n')
		for _, goal := range wakeupState.DroppedGoals {
			fmt.Fprintf(&output, "- %s (goal_id: %d)\n", oneLine(domain.Headline(goal.Content)), goal.ID)
		}
	}
	if len(wakeupState.UnclaimedDoingTasks) > 0 {
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(pendingUnclaimedDoingReason)
		output.WriteString("\n\n")
		output.WriteString(unclaimedDoingMarker)
		output.WriteByte('\n')
		for _, task := range wakeupState.UnclaimedDoingTasks {
			fmt.Fprintf(&output, "- %s (task_id: %d)\n", oneLine(task.Title), task.ID)
		}
	}
	return output.String(), nil
}

func pendingClaimReasonFor(lockCount, unstartedTaskCount, waitingAnswerTaskCount, availableTaskCount int) string {
	reason := fmt.Sprintf("You hold %d work locks.", lockCount)
	if availableTaskCount == 0 {
		return reason
	}
	return fmt.Sprintf("%s %d unstarted tasks in active goals (waiting for an answer: %d / available: %d). %d tasks in active goals have no work lock.\nIf you are waiting on a human, take one of those instead of stopping.", reason, unstartedTaskCount, waitingAnswerTaskCount, availableTaskCount, availableTaskCount)
}

func runPending(dir string) error {
	return runPendingForProject(dir, "", false)
}

func runPendingForProject(dir, projectName string, projectSpecified bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	output, exitCode, err := pendingCommandForProject(dir, cwd, projectName, projectSpecified)
	if err != nil {
		return err
	}
	if output != "" {
		if _, err := fmt.Fprint(os.Stdout, output); err != nil {
			return err
		}
	}
	if exitCode != 0 {
		return errNoPendingDecisions
	}
	return nil
}
