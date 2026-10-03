package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

var specFilePrefixes = []string{"doc/specs/", "doc/plans/", "docs/superpowers/"}

// goalBranchName is the branch script/worktree-setup.sh creates for a goal.
func goalBranchName(goalID int64) string { return "wt/goal-" + strconv.FormatInt(goalID, 10) }

// gitRunner runs git in repoRoot, bounded by the context.
type gitRunner struct {
	ctx      context.Context
	repoRoot string
}

func (g gitRunner) run(args ...string) ([]byte, error) {
	return exec.CommandContext(g.ctx, "git", append([]string{"-C", g.repoRoot}, args...)...).Output()
}

func (g gitRunner) verify(ref string) bool {
	_, err := g.run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// base resolves main (else master), each preferring refs/heads over
// refs/remotes/origin; "" when none exists.
func (g gitRunner) base() string {
	for _, name := range []string{"main", "master"} {
		for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
			if g.verify(ref) {
				return ref
			}
		}
	}
	return ""
}

// goalBranchAddedSpecFiles lists files the goal branch adds under
// doc/specs/, doc/plans/ or docs/superpowers/ relative to main (else master,
// each preferring refs/heads over refs/remotes/origin). Spec and plan live in
// the goal fields, so such files mean they leaked out to disk.
// ponytail: fail open — any git failure or missing repo, branch or base reports
// nothing so a broken environment never blocks the goal completion path.
func goalBranchAddedSpecFiles(ctx context.Context, repoRoot string, goalID int64) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	g := gitRunner{ctx, repoRoot}
	branch := "refs/heads/" + goalBranchName(goalID)
	if !g.verify(branch) {
		return nil, nil
	}
	base := g.base()
	if base == "" {
		return nil, nil
	}
	out, err := g.run("diff", "--name-only", "--diff-filter=A", "-z", base+"..."+branch)
	if err != nil {
		return nil, nil
	}
	var found []string
	for _, f := range strings.Split(string(out), "\x00") {
		for _, prefix := range specFilePrefixes {
			if strings.HasPrefix(f, prefix) {
				found = append(found, f)
				break
			}
		}
	}
	sort.Strings(found)
	return found, nil
}

// goalBranchMissingBase reports the base ref (main, else master) when the goal
// branch exists but does not contain it.
// ponytail: fail open — only git's exit code 1 counts; any other failure, missing repo, branch or base reports false.
func goalBranchMissingBase(ctx context.Context, repoRoot string, goalID int64) (base string, missing bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	g := gitRunner{ctx, repoRoot}
	branch := "refs/heads/" + goalBranchName(goalID)
	if !g.verify(branch) {
		return "", false
	}
	base = g.base()
	if base == "" {
		return "", false
	}
	_, err := g.run("merge-base", "--is-ancestor", base, branch)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return base, true
	}
	return base, false
}

// refuseGoalBranchProblems rejects a goal handoff review request while the goal
// branch lacks main or carries spec/plan files; those belong in the goal fields.
func (d *Daemon) refuseGoalBranchProblems(ctx context.Context, goalID int64) error {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return err
	}
	projects, err := d.store.ListProjects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.ID != goal.ProjectID {
			continue
		}
		if base, missing := goalBranchMissingBase(ctx, p.RootPath, goalID); missing {
			short := strings.TrimPrefix(strings.TrimPrefix(base, "refs/heads/"), "refs/remotes/")
			return fmt.Errorf("goal %d branch %s does not contain %s; run `git merge main --no-edit` in the goal worktree, re-run the build/vet/test/schema-check/wrapper checks, then request review again", goalID, goalBranchName(goalID), short)
		}
		files, err := goalBranchAddedSpecFiles(ctx, p.RootPath, goalID)
		if err != nil {
			return err
		}
		if len(files) > 0 {
			return fmt.Errorf("goal %d branch adds spec/plan files (%s); move the spec and plan in full into the goal fields with atct_goal_update_request_report, then `git rm` those files and commit before requesting review again", goalID, strings.Join(files, ", "))
		}
	}
	return nil
}
