package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

var specFilePrefixes = []string{"doc/specs/", "doc/plans/", "docs/superpowers/"}

// goal8 is the goal number script/worktree-setup.sh uses for the worktree
// directory and branch name: the decimal id cut to 8 characters.
func goal8(goalID int64) string {
	s := strconv.FormatInt(goalID, 10)
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

func goalBranchName(goalID int64) string { return "wt/goal-" + goal8(goalID) }

// goalBranchAddedSpecFiles lists files the goal branch adds under
// doc/specs/, doc/plans/ or docs/superpowers/ relative to main (else master,
// each preferring refs/heads over refs/remotes/origin). Spec and plan live in
// the goal fields, so such files mean they leaked out to disk.
// ponytail: fail open — any git failure or missing repo, branch or base reports
// nothing so a broken environment never blocks the goal completion path.
func goalBranchAddedSpecFiles(ctx context.Context, repoRoot string, goalID int64) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	git := func(args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "git", append([]string{"-C", repoRoot}, args...)...).Output()
	}
	verify := func(ref string) bool {
		_, err := git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
		return err == nil
	}
	branch := "refs/heads/" + goalBranchName(goalID)
	if !verify(branch) {
		return nil, nil
	}
	base := ""
	for _, name := range []string{"main", "master"} {
		for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
			if base == "" && verify(ref) {
				base = ref
			}
		}
	}
	if base == "" {
		return nil, nil
	}
	out, err := git("diff", "--name-only", "--diff-filter=A", "-z", base+"..."+branch)
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

// refuseAddedSpecFiles rejects a goal handoff review request while the goal
// branch carries spec/plan files; they belong in the goal fields.
func (d *Daemon) refuseAddedSpecFiles(ctx context.Context, goalID int64) error {
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
