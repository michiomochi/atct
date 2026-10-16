package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

// legacyGoalNextAction is GoalNextAction as it read before the batch reads: one
// query per table for this goal, then deriveNextAction. It is the oracle for the
// batch path, so it does not share nextActionFor or loadAllGoalRows.
func legacyGoalNextAction(ctx context.Context, s *Store, goal domain.Goal) (NextAction, error) {
	if goal.Status == domain.GoalDone || goal.Status == domain.GoalDropped {
		return deriveNextAction(nextActionInput{Status: goal.Status}), nil
	}
	in := nextActionInput{Status: goal.Status}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		return NextAction{}, err
	}
	if len(decisions) > 0 {
		in.OpenDecision = &decisions[0]
	}
	if in.PendingState, err = s.GoalPendingState(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}
	goalHandoffs, err := s.ListGoalHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, err
	}
	for i := range goalHandoffs {
		if h := goalHandoffs[i]; h.CompletedReportAt == nil && h.RecoveredAt == nil {
			in.GoalHandoff = &goalHandoffs[i]
		}
	}
	planHandoffs, err := s.ListPlanHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, err
	}
	if len(planHandoffs) > 0 {
		in.PlanHandoff = &planHandoffs[len(planHandoffs)-1]
	}
	taskCreates, err := s.ListTaskCreateHandoffs(ctx, goal.ID)
	if err != nil {
		return NextAction{}, err
	}
	for i := range taskCreates {
		if h := taskCreates[i]; h.CompletedAt == nil && h.RecoveredAt == nil {
			in.TaskCreate = &taskCreates[i]
		}
	}
	if in.Tasks, err = s.ListTasks(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}
	if in.TaskHandoffs, err = s.ListOpenTaskHandoffsForGoal(ctx, goal.ID); err != nil {
		return NextAction{}, err
	}
	return deriveNextAction(in), nil
}

// legacyGoalAwaitsHandoffReview is the review-wait predicate before the batch read:
// a goal or plan handoff whose review was requested and is not yet received,
// rejected, or completed.
func legacyGoalAwaitsHandoffReview(ctx context.Context, s *Store, goalID int64) (bool, error) {
	goalHandoffs, err := s.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return false, err
	}
	for _, h := range goalHandoffs {
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil &&
			h.ReviewRejectedAt == nil && h.CompletedReportAt == nil && h.RecoveredAt == nil {
			return true, nil
		}
	}
	planHandoffs, err := s.ListPlanHandoffs(ctx, goalID)
	if err != nil {
		return false, err
	}
	for _, h := range planHandoffs {
		if h.ReviewRequestedAt != nil && h.ReviewReceivedAt == nil &&
			h.ReviewRejectedAt == nil && h.CompletedReportAt == nil {
			return true, nil
		}
	}
	return false, nil
}

// inboxOracle is a populated project. Every goal is named by its label, and each
// label states the step its derivation must give (inboxOracleCodes), so the
// oracle checks the expected result as well as agreement between the two reads.
type inboxOracle struct {
	t         *testing.T
	s         *Store
	ctx       context.Context
	project   int64
	commander int64
	sub       int64
	goals     map[string]int64
}

// inboxOracleCodes is the step each labelled goal must derive to.
var inboxOracleCodes = map[string]string{
	"plain":             "commander_request_handoff",
	"empty":             "subcommander_plan",
	"review":            "commander_receive_goal_review",
	"decision":          "human_decision",
	"attention":         "human_decision",
	"tasks-review":      "subcommander_review_task",
	"tasks-delegate":    "subcommander_delegate_task",
	"tasks-receive":     "executor_receive_task",
	"tasks-doing":       "executor_implement",
	"tasks-alldone":     "subcommander_request_goal_review",
	"tasks-dropped":     "subcommander_plan",
	"planreq":           "commander_receive_plan_review",
	"planrecv":          "commander_review_plan",
	"planrej":           "subcommander_receive_plan_rejection",
	"taskcreate":        "subcommander_receive_task_create",
	"createtasks":       "subcommander_create_tasks",
	"fromplan":          "subcommander_delegate_task",
	"approved-applied":  "commander_complete",
	"approved-answered": "commander_review_goal",
	"rejected-pending":  "commander_reject_handoff",
	"rejected-handoff":  "subcommander_receive_rejection",
	"resubmitted":       "commander_review_goal",
	"done":              "done",
	"dropped":           "withdrawn",
}

// inboxOracleAwaiting is the goals whose handoff review is waiting for the commander.
var inboxOracleAwaiting = map[string]bool{"review": true, "planreq": true}

// inboxOraclePending is the pending state each goal must read, "" for none.
var inboxOraclePending = map[string]string{
	"approved-applied": GoalPendingApprovedComplete,
	"rejected-pending": GoalPendingRejectedHandoffReject,
}

func buildInboxOracle(t *testing.T) *inboxOracle {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "inbox-oracle", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	o := &inboxOracle{t: t, s: s, ctx: ctx, project: project.ID, goals: map[string]int64{}}
	first := o.goal("plain")
	addLiveProjectClaim(t, s, first, "oracle-commander")
	o.commander = testSessionID("oracle-commander")
	addTestAgentSession(t, s, "oracle-sub")
	o.sub = testSessionID("oracle-sub")

	o.mustGoal("empty", o.handedOff)
	o.mustGoal("review", o.reviewRequested)
	o.mustGoal("decision", o.decisionGoal)
	o.taskGoal("attention", "one")
	o.taskGoal("tasks-review", "review")
	o.taskGoal("tasks-delegate", "delegate")
	o.taskGoal("tasks-receive", "receive")
	o.taskGoal("tasks-doing", "doing")
	o.taskGoal("tasks-alldone", "done")
	o.taskGoal("tasks-dropped", "dropped")
	o.mustGoal("planreq", o.planRequested)
	o.mustGoal("planrecv", o.planReceived)
	o.mustGoal("planrej", o.planRejected)
	o.mustGoal("taskcreate", o.planCompleted)
	o.mustGoal("createtasks", o.taskCreateReceived)
	o.mustGoal("fromplan", o.tasksFromPlan)
	o.mustGoal("approved-applied", o.goalReviewed)
	o.mustGoal("approved-answered", o.goalReviewed)
	o.mustGoal("rejected-pending", o.goalReviewRejected)
	o.mustGoal("rejected-handoff", o.handoffRejected)
	o.mustGoal("resubmitted", o.handoffResubmitted)
	o.statusGoal("done", "done")
	o.statusGoal("dropped", "dropped")
	o.approve("approved-applied", true)
	o.approve("approved-answered", false)
	o.archivedGoal()
	return o
}

// goal creates a labelled goal with no state.
func (o *inboxOracle) goal(label string) int64 {
	o.t.Helper()
	g, err := o.s.CreateGoal(o.ctx, o.project, "oracle "+label, "human")
	if err != nil {
		o.t.Fatalf("CreateGoal %s: %v", label, err)
	}
	o.goals[label] = g.ID
	return g.ID
}

// mustGoal creates a goal and runs its setup.
func (o *inboxOracle) mustGoal(label string, setup func(label string, goalID int64)) {
	o.t.Helper()
	setup(label, o.goal(label))
}

// handedOff gives the goal to the subcommander: a goal handoff, received.
func (o *inboxOracle) handedOff(label string, goalID int64) {
	o.t.Helper()
	if _, err := o.s.RequestGoalHandoff(o.ctx, label+"-gh", goalID, o.commander, "delegate"); err != nil {
		o.t.Fatalf("RequestGoalHandoff %s: %v", label, err)
	}
	if _, err := o.s.ReceiveGoalHandoff(o.ctx, label+"-gh", goalID, o.sub); err != nil {
		o.t.Fatalf("ReceiveGoalHandoff %s: %v", label, err)
	}
}

// reviewRequested is handedOff with the goal handoff review requested, not received.
func (o *inboxOracle) reviewRequested(label string, goalID int64) {
	o.t.Helper()
	o.handedOff(label, goalID)
	if _, err := o.s.RequestGoalHandoffReview(o.ctx, label+"-gh", goalID, o.sub, "ready"); err != nil {
		o.t.Fatalf("RequestGoalHandoffReview %s: %v", label, err)
	}
}

// reviewReceived is reviewRequested with the commander having received the review.
func (o *inboxOracle) reviewReceived(label string, goalID int64) {
	o.t.Helper()
	o.reviewRequested(label, goalID)
	if _, err := o.s.ReceiveGoalHandoffReview(o.ctx, label+"-gh", goalID, o.commander); err != nil {
		o.t.Fatalf("ReceiveGoalHandoffReview %s: %v", label, err)
	}
}

// planRequested is handedOff with a plan handoff review requested, not received.
func (o *inboxOracle) planRequested(label string, goalID int64) {
	o.t.Helper()
	o.handedOff(label, goalID)
	if _, err := o.s.UpdateGoalRequestReport(o.ctx, goalID, "# Spec", "# Plan"); err != nil {
		o.t.Fatalf("UpdateGoalRequestReport %s: %v", label, err)
	}
	if _, err := o.s.RequestPlanHandoffReview(o.ctx, label+"-plan", goalID, o.sub, "plan ready"); err != nil {
		o.t.Fatalf("RequestPlanHandoffReview %s: %v", label, err)
	}
}

// planReceived is planRequested with the commander having received the plan review.
func (o *inboxOracle) planReceived(label string, goalID int64) {
	o.t.Helper()
	o.planRequested(label, goalID)
	if _, err := o.s.ReceivePlanHandoffReview(o.ctx, label+"-plan", goalID, o.commander); err != nil {
		o.t.Fatalf("ReceivePlanHandoffReview %s: %v", label, err)
	}
}

// planRejected is planReceived with the plan rejected, not yet received by the subcommander.
func (o *inboxOracle) planRejected(label string, goalID int64) {
	o.t.Helper()
	o.planReceived(label, goalID)
	if _, err := o.s.RejectPlanHandoffReview(o.ctx, label+"-plan", goalID, o.commander, "revise the plan"); err != nil {
		o.t.Fatalf("RejectPlanHandoffReview %s: %v", label, err)
	}
}

// planCompleted accepts the plan, which opens the task-create handoff.
func (o *inboxOracle) planCompleted(label string, goalID int64) {
	o.t.Helper()
	o.planReceived(label, goalID)
	if _, err := o.s.CompletePlanHandoff(o.ctx, label+"-plan", goalID, o.commander, "plan accepted"); err != nil {
		o.t.Fatalf("CompletePlanHandoff %s: %v", label, err)
	}
}

// taskCreateReceived is planCompleted with the subcommander having received the task-create handoff.
func (o *inboxOracle) taskCreateReceived(label string, goalID int64) {
	o.t.Helper()
	o.planCompleted(label, goalID)
	tc, err := o.s.GetTaskCreateHandoffForGoal(o.ctx, goalID)
	if err != nil {
		o.t.Fatalf("GetTaskCreateHandoffForGoal %s: %v", label, err)
	}
	if _, err := o.s.ReceiveTaskCreateHandoff(o.ctx, tc.ID, o.sub); err != nil {
		o.t.Fatalf("ReceiveTaskCreateHandoff %s: %v", label, err)
	}
}

// tasksFromPlan creates the tasks through the task-create handoff, which completes it.
func (o *inboxOracle) tasksFromPlan(label string, goalID int64) {
	o.t.Helper()
	o.taskCreateReceived(label, goalID)
	tc, err := o.s.GetTaskCreateHandoffForGoal(o.ctx, goalID)
	if err != nil {
		o.t.Fatalf("GetTaskCreateHandoffForGoal %s: %v", label, err)
	}
	if _, err := o.s.CreateTasksForHandoff(o.ctx, tc.ID, o.sub, goalID, "worker", "oracle-fromplan", []string{"from plan"}, []string{"desc from plan"}); err != nil {
		o.t.Fatalf("CreateTasksForHandoff %s: %v", label, err)
	}
}

// goalReviewed asks the goal review on a goal whose handoff review the commander received.
func (o *inboxOracle) goalReviewed(label string, goalID int64) {
	o.t.Helper()
	o.reviewReceived(label, goalID)
	if _, err := o.s.RequestGoalReview(o.ctx, goalID, o.commander, inboxOracleReport()); err != nil {
		o.t.Fatalf("RequestGoalReview %s: %v", label, err)
	}
}

// goalReviewRejected rejects the goal review but not yet the goal handoff.
func (o *inboxOracle) goalReviewRejected(label string, goalID int64) {
	o.t.Helper()
	o.goalReviewed(label, goalID)
	if err := o.s.RejectGoalReview(o.ctx, o.latestGoalReviewID(goalID), "needs another pass"); err != nil {
		o.t.Fatalf("RejectGoalReview %s: %v", label, err)
	}
}

// handoffRejected also rejects the goal handoff review, which the subcommander has yet to receive.
func (o *inboxOracle) handoffRejected(label string, goalID int64) {
	o.t.Helper()
	o.goalReviewRejected(label, goalID)
	if _, err := o.s.RejectGoalHandoffReview(o.ctx, label+"-gh", goalID, o.commander, "revise the goal"); err != nil {
		o.t.Fatalf("RejectGoalHandoffReview %s: %v", label, err)
	}
}

// handoffResubmitted receives the rejection and resubmits the review; the commander has received it again.
func (o *inboxOracle) handoffResubmitted(label string, goalID int64) {
	o.t.Helper()
	o.handoffRejected(label, goalID)
	if _, err := o.s.ReceiveGoalHandoffReviewRejection(o.ctx, label+"-gh", goalID, o.sub); err != nil {
		o.t.Fatalf("ReceiveGoalHandoffReviewRejection %s: %v", label, err)
	}
	if _, err := o.s.RequestGoalHandoffReview(o.ctx, label+"-gh", goalID, o.sub, "revised"); err != nil {
		o.t.Fatalf("resubmit RequestGoalHandoffReview %s: %v", label, err)
	}
	if _, err := o.s.ReceiveGoalHandoffReview(o.ctx, label+"-gh", goalID, o.commander); err != nil {
		o.t.Fatalf("ReceiveGoalHandoffReview resubmitted %s: %v", label, err)
	}
}

// approve answers the goal review approve; applied marks it applied, as the
// listing pass does once the answer is taken up.
func (o *inboxOracle) approve(label string, applied bool) {
	o.t.Helper()
	goalID := o.goals[label]
	decisionID := o.latestGoalReviewID(goalID)
	if _, err := o.s.AnswerDecision(o.ctx, AnswerInput{DecisionID: decisionID, AnswerLabel: "approve"}); err != nil {
		o.t.Fatalf("AnswerDecision approve %s: %v", label, err)
	}
	if applied {
		if _, err := o.s.DB().ExecContext(o.ctx, `UPDATE decisions SET status = ?, applied_at = ? WHERE id = ?`, string(domain.DecisionApplied), formatTimestamp(time.Now()), decisionID); err != nil {
			o.t.Fatalf("mark applied %s: %v", label, err)
		}
	}
}

// decisionGoal asks a goal-level question on a handed-off goal.
func (o *inboxOracle) decisionGoal(label string, goalID int64) {
	o.t.Helper()
	o.handedOff(label, goalID)
	if _, err := o.s.AskDecision(o.ctx, AskInput{GoalID: goalID, Kind: domain.DecisionKind("question"), Question: "Which?", AgentSessionID: o.commander}); err != nil {
		o.t.Fatalf("AskDecision goal %s: %v", label, err)
	}
}

// taskGoal hands the goal off and gives it one task in the state the label names.
// "attention" asks a question on its task, the rest set the task status.
func (o *inboxOracle) taskGoal(label, state string) {
	o.t.Helper()
	goalID := o.goal(label)
	o.handedOff(label, goalID)
	tasks, err := o.s.CreateTasks(o.ctx, goalID, "worker", "oracle-"+label, []string{"one", "two"}, []string{"desc one", "desc two"})
	if err != nil {
		o.t.Fatalf("CreateTasks %s: %v", label, err)
	}
	first, second := tasks[0], tasks[1]
	switch state {
	case "one":
		if _, err := o.s.AskDecision(o.ctx, AskInput{GoalID: goalID, TaskID: first.ID, Kind: domain.DecisionKind("question"), Question: "Which?", AgentSessionID: o.commander}); err != nil {
			o.t.Fatalf("AskDecision task %s: %v", label, err)
		}
	case "review":
		o.setTask(first.ID, "review")
	case "delegate":
		// both tasks stay todo with no handoff
	case "receive":
		if _, err := o.s.RequestTaskHandoff(o.ctx, label+"-task", first.ID, o.sub, "work"); err != nil {
			o.t.Fatalf("RequestTaskHandoff %s: %v", label, err)
		}
		o.setTask(second.ID, "dropped")
	case "doing":
		o.setTask(first.ID, "doing")
		o.setTask(second.ID, "dropped")
	case "done":
		o.setTask(first.ID, "done")
		o.setTask(second.ID, "done")
	case "dropped":
		o.setTask(first.ID, "dropped")
		o.setTask(second.ID, "dropped")
	default:
		o.t.Fatalf("unknown task state %q", state)
	}
}

// statusGoal sets a finished goal's status, as the existing tests do.
func (o *inboxOracle) statusGoal(label, status string) {
	o.t.Helper()
	goalID := o.goal(label)
	if _, err := o.s.DB().ExecContext(o.ctx, `UPDATE goals SET status = ?, work_done = 'w', now_possible = 'n', how_to_verify = 'v', surprises = 's', needs_review = 'r' WHERE id = ?`, status, goalID); err != nil {
		o.t.Fatalf("set status %s: %v", label, err)
	}
}

// archivedGoal is a goal with an open decision in a project that is then archived:
// it must be in no list the inbox reads.
func (o *inboxOracle) archivedGoal() {
	o.t.Helper()
	project, err := o.s.CreateProject(o.ctx, "archived-oracle", o.t.TempDir())
	if err != nil {
		o.t.Fatalf("CreateProject archived: %v", err)
	}
	goal, err := o.s.CreateGoal(o.ctx, project.ID, "archived goal", "human")
	if err != nil {
		o.t.Fatalf("CreateGoal archived: %v", err)
	}
	if _, err := o.s.AskDecision(o.ctx, AskInput{GoalID: goal.ID, Kind: domain.DecisionKind("question"), Question: "Hidden?", AgentSessionID: o.commander}); err != nil {
		o.t.Fatalf("AskDecision archived: %v", err)
	}
	if _, err := o.s.ArchiveProject(o.ctx, project.ID); err != nil {
		o.t.Fatalf("ArchiveProject: %v", err)
	}
	o.goals["archived"] = goal.ID
}

func (o *inboxOracle) setTask(taskID int64, status string) {
	o.t.Helper()
	if _, err := o.s.DB().ExecContext(o.ctx, `UPDATE tasks SET status = ? WHERE id = ?`, status, taskID); err != nil {
		o.t.Fatalf("set task %d to %s: %v", taskID, status, err)
	}
}

// latestGoalReviewID is the goal's open or answered goal_review decision, the latest one.
func (o *inboxOracle) latestGoalReviewID(goalID int64) int64 {
	o.t.Helper()
	decisions, err := o.s.ListDecisionsForGoal(o.ctx, goalID)
	if err != nil {
		o.t.Fatalf("ListDecisionsForGoal: %v", err)
	}
	var id int64
	for _, d := range decisions {
		if d.Kind == domain.KindGoalReview {
			id = d.ID
		}
	}
	if id == 0 {
		o.t.Fatalf("goal %d has no goal review decision", goalID)
	}
	return id
}

func inboxOracleReport() domain.CompletionReport {
	return domain.CompletionReport{WorkDone: "w", NowPossible: "n", HowToVerify: "v", Surprises: "s", NeedsReview: "r"}
}

// TestInboxOracleMatchesPopulatedDerivation checks the batch reads against the
// pre-batch oracle over every goal of a populated project: the next action, the
// review wait, and the pending state, including the reject path where the
// goal handoff is read late. It also checks each goal against the step its label names.
func TestInboxOracleMatchesPopulatedDerivation(t *testing.T) {
	o := buildInboxOracle(t)
	ctx := o.ctx
	s := o.s

	goals, err := s.ListAllGoals(ctx)
	if err != nil {
		t.Fatalf("ListAllGoals: %v", err)
	}
	byID := make(map[int64]domain.Goal, len(goals))
	var active []domain.Goal
	for _, g := range goals {
		byID[g.ID] = g
		if g.Status == domain.GoalActive {
			active = append(active, g)
		}
	}
	if _, listed := byID[o.goals["archived"]]; listed {
		t.Fatalf("goal in an archived project is listed")
	}
	inboxGoals, err := s.ListAllGoalsForInbox(ctx)
	if err != nil {
		t.Fatalf("ListAllGoalsForInbox: %v", err)
	}
	for _, g := range inboxGoals {
		if g.ID == o.goals["archived"] {
			t.Fatalf("archived goal %d is in the inbox goal list", g.ID)
		}
	}

	batch, err := s.GoalNextActions(ctx, active)
	if err != nil {
		t.Fatalf("GoalNextActions: %v", err)
	}
	awaiting, err := s.GoalsAwaitingHandoffReview(ctx)
	if err != nil {
		t.Fatalf("GoalsAwaitingHandoffReview: %v", err)
	}
	rows, err := s.loadAllGoalRows(ctx)
	if err != nil {
		t.Fatalf("loadAllGoalRows: %v", err)
	}

	for label, goalID := range o.goals {
		if label == "archived" {
			continue
		}
		goal := byID[goalID]
		want, err := legacyGoalNextAction(ctx, s, goal)
		if err != nil {
			t.Fatalf("legacy next action %s: %v", label, err)
		}
		if goal.Status == domain.GoalActive {
			if !reflect.DeepEqual(batch[goalID], want) {
				t.Fatalf("%s: batch next action %+v, legacy %+v", label, batch[goalID], want)
			}
		} else {
			public, err := s.GoalNextAction(ctx, goal)
			if err != nil {
				t.Fatalf("GoalNextAction %s: %v", label, err)
			}
			if !reflect.DeepEqual(public, want) {
				t.Fatalf("%s: GoalNextAction %+v, legacy %+v", label, public, want)
			}
		}
		if want.Code != inboxOracleCodes[label] {
			t.Fatalf("%s: derived %q, want %q", label, want.Code, inboxOracleCodes[label])
		}

		legacyAwait, err := legacyGoalAwaitsHandoffReview(ctx, s, goalID)
		if err != nil {
			t.Fatalf("legacy awaiting %s: %v", label, err)
		}
		if legacyAwait != awaiting[goalID] || legacyAwait != inboxOracleAwaiting[label] {
			t.Fatalf("%s: awaiting review legacy=%v batch=%v, want %v", label, legacyAwait, awaiting[goalID], inboxOracleAwaiting[label])
		}

		perGoal, err := s.GoalPendingState(ctx, goalID)
		if err != nil {
			t.Fatalf("GoalPendingState %s: %v", label, err)
		}
		batchPending, err := goalPendingState(rows.decisions[goalID], func() ([]GoalHandoff, error) {
			return rows.goalHandoffs[goalID], nil
		})
		if err != nil {
			t.Fatalf("goalPendingState %s: %v", label, err)
		}
		if perGoal != batchPending || perGoal != inboxOraclePending[label] {
			t.Fatalf("%s: pending per-goal %q, batch %q, want %q", label, perGoal, batchPending, inboxOraclePending[label])
		}
	}

	wantAwaiting := make(map[int64]bool, len(inboxOracleAwaiting))
	for label := range inboxOracleAwaiting {
		wantAwaiting[o.goals[label]] = true
	}
	if !reflect.DeepEqual(awaiting, wantAwaiting) {
		t.Fatalf("awaiting review = %v, want %v", awaiting, wantAwaiting)
	}
}
