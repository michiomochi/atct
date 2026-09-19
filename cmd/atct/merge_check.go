package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// goalBranchWriters are the git subcommands that move a goal branch's history
// onto the branch you are standing on. Reading, diffing, and deleting the
// branch do not, so they are absent: the gate is about what lands on main, not
// about naming the branch.
var goalBranchWriters = map[string]bool{
	"merge":       true,
	"cherry-pick": true,
	"rebase":      true,
	"pull":        true,
}

var goalBranchPattern = regexp.MustCompile(`\bwt/goal-(\d+)\b`)

// shellSegments splits on the separators that start a new command, so the git
// invocation is the one the branch name actually belongs to.
var shellSegments = regexp.MustCompile(`&&|\|\||[;|()]|\n`)

// goalBranchMergedIntoCurrentBranch returns the goal whose branch the command
// would merge into the current branch, or 0 when the command does no such
// thing. It reads the command text because that is all a PreToolUse hook has.
func goalBranchMergedIntoCurrentBranch(command string) int64 {
	match := goalBranchPattern.FindStringSubmatch(command)
	if match == nil {
		return 0
	}
	goalID, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || goalID <= 0 {
		return 0
	}
	head := command[:strings.Index(command, match[0])]
	segments := shellSegments.Split(head, -1)
	if !gitSubcommandWrites(segments[len(segments)-1]) {
		return 0
	}
	return goalID
}

// gitSubcommandWrites reports whether a segment invokes git with one of the
// writing subcommands. Requiring git in command position keeps
// `echo git merge wt/goal-283` from reading as a merge.
func gitSubcommandWrites(segment string) bool {
	fields := strings.Fields(segment)
	for len(fields) > 0 && strings.Contains(fields[0], "=") {
		fields = fields[1:] // leading environment assignments
	}
	if len(fields) == 0 {
		return false
	}
	if fields[0] != "git" && !strings.HasSuffix(fields[0], "/git") {
		return false
	}
	for i := 1; i < len(fields); i++ {
		switch {
		case fields[i] == "-C" || fields[i] == "-c":
			i++ // the option's argument is not the subcommand
		case strings.HasPrefix(fields[i], "-"):
		default:
			return goalBranchWriters[fields[i]]
		}
	}
	return false
}

// goalReviewApproved reports whether the human answered this goal's review
// with approve. That answer is the only thing that permits a merge to main,
// so anything else -- rejected, unanswered, no review at all -- is a refusal.
func goalReviewApproved(decisions []domain.Decision) bool {
	for _, decision := range decisions {
		if decision.Kind != domain.KindGoalReview {
			continue
		}
		if decision.Status == domain.DecisionApplied && decision.AnswerLabel == "approve" {
			return true
		}
	}
	return false
}

func runMergeCheck(dir string) error {
	input, err := decodeMergeHookInput(os.Stdin)
	if err != nil {
		// A gate that cannot read its input must not become a gate that lets
		// everything through.
		_, writeErr := fmt.Fprint(os.Stdout, mergeCheckDeny("ATCT merge-check could not read the tool call: "+err.Error()))
		return writeErr
	}
	goalID := goalBranchMergedIntoCurrentBranch(input.ToolInput.Command)
	if goalID == 0 {
		return nil
	}
	reason, denied := mergeCheckRefusal(dir, goalID)
	if !denied {
		return nil
	}
	_, err = fmt.Fprint(os.Stdout, mergeCheckDeny(reason))
	return err
}

type mergeHookInput struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

func decodeMergeHookInput(r io.Reader) (mergeHookInput, error) {
	var input mergeHookInput
	payload, err := io.ReadAll(r)
	if err != nil {
		return input, err
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return input, nil
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		return input, err
	}
	return input, nil
}

func mergeCheckRefusal(dir string, goalID int64) (string, bool) {
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		return fmt.Sprintf("ATCT merge-check could not read goal %d's review: %v", goalID, err), true
	}
	defer s.Close()
	decisions, err := s.ListDecisionsForGoal(context.Background(), goalID)
	if err != nil {
		return fmt.Sprintf("ATCT merge-check could not read goal %d's review: %v", goalID, err), true
	}
	if goalReviewApproved(decisions) {
		return "", false
	}
	return fmt.Sprintf(
		"Goal %d has not been approved by the human, so wt/goal-%d must not be merged into main. "+
			"Human approval of atct_goal_review_request is the only condition that permits this merge: "+
			"a passing commander review, finished tasks, and green tests are not substitutes. "+
			"Call atct_goal_review_request and wait for the goal_review decision to be applied with approve.",
		goalID, goalID), true
}

func mergeCheckDeny(reason string) string {
	payload, err := json.Marshal(preToolUseOutput{HookSpecificOutput: preToolUseHookSpecificOutput{
		HookEventName:            "PreToolUse",
		PermissionDecision:       "deny",
		PermissionDecisionReason: reason,
	}})
	if err != nil {
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"ATCT merge-check failed"}}`
	}
	return string(payload)
}
