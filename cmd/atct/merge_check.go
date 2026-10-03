package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	return gitSubcommandIn(segment, goalBranchWriters)
}

// headMovers are the git subcommands that change which branch HEAD is on.
var headMovers = map[string]bool{"checkout": true, "switch": true}

func gitSubcommandIn(segment string, set map[string]bool) bool {
	fields, _ := commandFields(segment)
	if len(fields) == 0 {
		return false
	}
	if fields[0] != "git" && !strings.HasSuffix(fields[0], "/git") {
		return false
	}
	for i := 1; i < len(fields); i++ {
		switch {
		case fields[i] == "-C" || fields[i] == "-c" || fields[i] == "--git-dir" || fields[i] == "--work-tree":
			i++ // the option's argument is not the subcommand
		case strings.HasPrefix(fields[i], "-"):
		default:
			return set[fields[i]]
		}
	}
	return false
}

// commandFields returns a segment's fields starting at the command word, after
// dropping group openers (`{`, `(`), environment assignments and `env` with its
// options. `export` is dropped too, so `export GIT_DIR=...` counts as an
// assignment. redirected reports that the prefix chose the repository itself
// (GIT_DIR=, GIT_WORK_TREE=, env -C), which the guard cannot follow.
func commandFields(segment string) (fields []string, redirected bool) {
	fields = strings.Fields(strings.TrimLeft(segment, " \t({"))
	for len(fields) > 0 {
		f := fields[0]
		switch {
		case f == "{" || f == "(" || f == "export":
		case strings.HasPrefix(f, "GIT_DIR=") || strings.HasPrefix(f, "GIT_WORK_TREE="):
			redirected = true
		case strings.Contains(f, "=") && !strings.HasPrefix(f, "-"):
		case f == "env":
			for len(fields) > 1 && strings.HasPrefix(fields[1], "-") {
				if fields[1] == "-C" || strings.HasPrefix(fields[1], "--chdir") {
					redirected = true
				}
				if fields[1] == "-C" || fields[1] == "-u" || fields[1] == "-S" {
					fields = fields[1:] // skip the option's argument too
				}
				fields = fields[1:]
			}
		default:
			return fields, redirected
		}
		fields = fields[1:]
	}
	return nil, redirected
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
	reason, denied := mergeCheckDecision(dir, input.CWD, input.ToolInput.Command)
	if !denied {
		return nil
	}
	_, err = fmt.Fprint(os.Stdout, mergeCheckDeny(reason))
	return err
}

// mergeCheckDecision refuses only a goal-branch merge that lands on main/master.
// Merging into a worktree branch or a detached HEAD (a throwaway trial worktree)
// does not move main, so it passes. A HEAD that cannot be determined is refused.
func mergeCheckDecision(dir, cwd, command string) (string, bool) {
	goalID := goalBranchMergedIntoCurrentBranch(command)
	if goalID == 0 {
		return "", false
	}
	gitC, moved := goalBranchGitContext(command)
	if moved {
		return fmt.Sprintf("ATCT merge-check cannot tell which branch wt/goal-%d would be merged into, "+
			"because the command changes directory or switches branch (cd, pushd, git checkout, git switch) or picks the repository by environment or option (GIT_DIR=, GIT_WORK_TREE=, env -C, git --git-dir, git --work-tree) before the merge. "+
			"Do not cd or switch; use `git -C <path>` on a work tree that already has the target HEAD.", goalID), true
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if cwd == "" {
		return fmt.Sprintf("ATCT merge-check could not determine the working directory for the merge of wt/goal-%d.", goalID), true
	}
	for _, p := range gitC {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		cwd = p
	}
	switch headKind(cwd) {
	case headOtherBranch, headDetached:
		return "", false
	}
	return mergeCheckRefusal(dir, goalID)
}

// goalBranchGitContext returns the `git -C` paths on the invocation that names
// the goal branch, and whether an earlier segment runs cd/pushd or git
// checkout/switch (which move the directory or HEAD; the guard does not track
// them).
func goalBranchGitContext(command string) (gitC []string, moved bool) {
	loc := goalBranchPattern.FindStringIndex(command)
	if loc == nil {
		return nil, false
	}
	segments := shellSegments.Split(command[:loc[0]], -1)
	for i, seg := range segments {
		fields, redirected := commandFields(seg)
		if redirected {
			moved = true
		}
		if i == len(segments)-1 {
			break
		}
		if len(fields) > 0 && (fields[0] == "cd" || fields[0] == "pushd") {
			moved = true
		}
		if gitSubcommandIn(seg, headMovers) {
			moved = true
		}
	}
	fields, _ := commandFields(segments[len(segments)-1])
	if gitOptionsPickRepository(fields) {
		moved = true
	}
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "-C" {
			i++
			gitC = append(gitC, strings.Trim(fields[i], `"'`))
		}
	}
	return gitC, moved
}

// gitOptionsPickRepository reports whether git's own options before the
// subcommand choose the repository (--git-dir, --work-tree, either spelling).
func gitOptionsPickRepository(fields []string) bool {
	if len(fields) == 0 || (fields[0] != "git" && !strings.HasSuffix(fields[0], "/git")) {
		return false
	}
	for _, f := range fields[1:] {
		if !strings.HasPrefix(f, "-") {
			return false
		}
		if f == "--git-dir" || f == "--work-tree" || strings.HasPrefix(f, "--git-dir=") || strings.HasPrefix(f, "--work-tree=") {
			return true
		}
	}
	return false
}

type headState int

const (
	headUnknown headState = iota
	headMain
	headOtherBranch
	headDetached
)

// headKind classifies HEAD of the work tree at dir. A failure of git itself is
// headUnknown, never a pass.
func headKind(dir string) headState {
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "-q", "HEAD").Output()
	name := strings.TrimSpace(string(out))
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 && name == "" {
			return headDetached
		}
		return headUnknown
	}
	switch name {
	case "main", "master":
		return headMain
	case "":
		return headUnknown
	}
	return headOtherBranch
}

type mergeHookInput struct {
	ToolName  string `json:"tool_name"`
	CWD       string `json:"cwd"`
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
