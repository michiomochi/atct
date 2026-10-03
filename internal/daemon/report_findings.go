package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

const findingsListMax = 10

// worktreeStatus splits `git status --porcelain` of the goal worktree into
// tracked-file changes and untracked paths.
// ponytail: fail open — a missing worktree or any git failure reports ok=false.
func worktreeStatus(ctx context.Context, projectRoot string, goalID int64) (tracked, untracked []string, ok bool) {
	worktree := filepath.Join(projectRoot, ".worktrees", strconv.FormatInt(goalID, 10))
	if info, err := os.Stat(worktree); err != nil || !info.IsDir() {
		return nil, nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := runWakeupGit(ctx, worktree, "status", "--porcelain", "-z")
	if err != nil {
		return nil, nil, false
	}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		if entry[:2] == "??" {
			untracked = append(untracked, entry[3:])
			continue
		}
		tracked = append(tracked, entry[3:])
		if strings.ContainsAny(entry[:2], "RC") {
			i++ // the next entry is the original path
		}
	}
	return tracked, untracked, true
}

// branchNotAheadOfBase reports true only when the goal branch and base both
// exist and the branch has no commit beyond base.
func branchNotAheadOfBase(ctx context.Context, projectRoot string, goalID int64) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	g := gitRunner{ctx, projectRoot}
	branch := "refs/heads/" + goalBranchName(goalID)
	if !g.verify(branch) {
		return false
	}
	base := g.base()
	if base == "" {
		return false
	}
	out, err := g.run("rev-list", "--count", base+".."+branch)
	return err == nil && strings.TrimSpace(string(out)) == "0"
}

// goalProjectRoot resolves the project root of a goal.
func (d *Daemon) goalProjectRoot(ctx context.Context, goalID int64) (string, error) {
	goal, err := d.store.GetGoal(ctx, goalID)
	if err != nil {
		return "", err
	}
	projects, err := d.store.ListProjects(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.ID == goal.ProjectID {
			return p.RootPath, nil
		}
	}
	return "", fmt.Errorf("project %d not found for goal %d", goal.ProjectID, goalID)
}

func firstPaths(paths []string) string {
	if len(paths) > findingsListMax {
		paths = paths[:findingsListMax]
	}
	return strings.Join(paths, ", ")
}

// goalReviewGate refuses a goal review request while tracked files are
// uncommitted; other findings come back as a note for appendFindings.
func (d *Daemon) goalReviewGate(ctx context.Context, goalID int64) (note string, err error) {
	root, err := d.goalProjectRoot(ctx, goalID)
	if err != nil {
		return "", err
	}
	tracked, untracked, ok := worktreeStatus(ctx, root, goalID)
	if !ok {
		return "", nil
	}
	if len(tracked) > 0 {
		list := firstPaths(tracked)
		if len(tracked) > findingsListMax {
			list += fmt.Sprintf(", and %d more", len(tracked)-findingsListMax)
		}
		return "", fmt.Errorf("goal %d worktree has uncommitted changes to tracked files (%s); commit them, then request review again", goalID, list)
	}
	var lines []string
	if len(untracked) > 0 {
		lines = append(lines, fmt.Sprintf("- worktree に未追跡ファイルが %d 件ある: %s (先頭 %d 件)", len(untracked), firstPaths(untracked), findingsListMax))
	}
	if tasks, err := d.store.ListTasks(ctx, goalID); err == nil {
		for _, t := range tasks {
			if t.Status == domain.TaskDone {
				if branchNotAheadOfBase(ctx, root, goalID) {
					lines = append(lines, "- done の task があるが、branch に main より新しい commit が無い")
				}
				break
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// taskReviewNote describes uncommitted or untracked files in the goal worktree;
// it never refuses and returns "" on any error.
func (d *Daemon) taskReviewNote(ctx context.Context, taskID int64) string {
	goalID, err := d.store.GetTaskGoalID(ctx, taskID)
	if err != nil {
		return ""
	}
	root, err := d.goalProjectRoot(ctx, goalID)
	if err != nil {
		return ""
	}
	tracked, untracked, ok := worktreeStatus(ctx, root, goalID)
	if !ok || len(tracked)+len(untracked) == 0 {
		return ""
	}
	all := append(tracked, untracked...)
	return fmt.Sprintf("- worktree に未コミットの変更または未追跡ファイルが %d 件ある: %s (先頭 %d 件)", len(all), firstPaths(all), findingsListMax)
}

// appendFindings adds the daemon-recorded note to a non-empty report.
func appendFindings(report, note string) string {
	if note == "" || strings.TrimSpace(report) == "" {
		return report
	}
	return report + "\n\n---\nATCT 検出（daemon 記録。報告者の記述ではない）:\n" + note
}
