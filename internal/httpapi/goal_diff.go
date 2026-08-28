package httpapi

import (
	"context"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const goalDiffPatchLineLimit = 2000

type goalDiffView struct {
	Available    bool                 `json:"available"`
	Reason       string               `json:"reason"`
	BaseRef      string               `json:"base_ref"`
	Branch       string               `json:"branch"`
	Source       string               `json:"source"`
	MergeCommit  string               `json:"merge_commit"`
	FilesChanged int                  `json:"files_changed"`
	Insertions   int                  `json:"insertions"`
	Deletions    int                  `json:"deletions"`
	Files        []taskCommitDiffFile `json:"files"`
	Path         string               `json:"path"`
	Patch        string               `json:"patch"`
	OmittedLines int                  `json:"omitted_lines"`
}

func (s *Server) handleGoalDiff(w http.ResponseWriter, r *http.Request, goalID string) {
	response := goalDiffView{Files: make([]taskCommitDiffFile, 0)}
	ctx := r.Context()
	if ctx.Err() != nil {
		response.Reason = "timeout"
		writeJSON(w, http.StatusOK, response)
		return
	}

	canonicalGoalID, ok := s.resolveGoalID(w, ctx, goalID)
	if !ok {
		return
	}
	goal, err := s.store.GetGoal(ctx, canonicalGoalID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	projectRootPath := ""
	for _, project := range projects {
		if project.ID == goal.ProjectID {
			projectRootPath = project.RootPath
			break
		}
	}
	if ctx.Err() != nil {
		response.Reason = "timeout"
		writeJSON(w, http.StatusOK, response)
		return
	}
	if projectRootPath == "" {
		response.Reason = "not_git"
		writeJSON(w, http.StatusOK, response)
		return
	}

	branch := goalWorktreeBranch(canonicalGoalID)
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := runGoalDiffGit(gitCtx, projectRootPath, "rev-parse", "--git-dir"); err != nil {
		response.Reason = goalDiffFailureReason(gitCtx, "not_git")
		writeJSON(w, http.StatusOK, response)
		return
	}

	baseRef, baseRevision, err := resolveGoalDiffBase(gitCtx, projectRootPath)
	if err != nil {
		response.Reason = goalDiffFailureReason(gitCtx, "no_base")
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.BaseRef = baseRef
	response.Branch = branch

	diffArgs := []string{baseRevision + "..." + branch}
	if _, err := runGoalDiffGit(gitCtx, projectRootPath, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
		mergeCommit, parents, resolveErr := goalDiffMergeCommitByMessage(gitCtx, projectRootPath, strconv.FormatInt(canonicalGoalID, 10), baseRevision)
		if resolveErr != nil {
			response.Reason = goalDiffFailureReason(gitCtx, "no_branch")
			writeJSON(w, http.StatusOK, response)
			return
		}
		if mergeCommit == "" {
			response.Reason = "no_branch"
			writeJSON(w, http.StatusOK, response)
			return
		}
		if parents < 2 {
			response.Reason = "merged_unresolved"
			writeJSON(w, http.StatusOK, response)
			return
		}
		diffArgs = []string{mergeCommit + "^1", mergeCommit}
		response.Source = "merge_commit"
		response.MergeCommit = mergeCommit
	} else {
		mergeCommit, resolveErr := goalDiffMergeCommitForBranch(gitCtx, projectRootPath, branch, baseRevision)
		if resolveErr != nil {
			response.Reason = goalDiffFailureReason(gitCtx, "diff_failed")
			writeJSON(w, http.StatusOK, response)
			return
		}
		response.Source = "branch"
		if mergeCommit != "" {
			diffArgs = []string{mergeCommit + "^1", mergeCommit}
			response.Source = "merge_commit"
			response.MergeCommit = mergeCommit
		}
	}

	path := r.URL.Query().Get("path")
	if path != "" {
		pathDiffArgs := make([]string, 0, len(diffArgs)+3)
		pathDiffArgs = append(pathDiffArgs, "diff")
		pathDiffArgs = append(pathDiffArgs, diffArgs...)
		pathDiffArgs = append(pathDiffArgs, "--", path)
		output, err := runGoalDiffGit(gitCtx, projectRootPath, pathDiffArgs...)
		if err != nil {
			response.Reason = goalDiffFailureReason(gitCtx, "diff_failed")
			writeJSON(w, http.StatusOK, response)
			return
		}
		response.Available = true
		response.Path = path
		lineCount := goalDiffLineCount(string(output))
		if lineCount <= goalDiffPatchLineLimit {
			response.Patch = string(output)
		} else {
			response.OmittedLines = lineCount
			response.Patch = ""
		}
		writeJSON(w, http.StatusOK, response)
		return
	}

	numstatDiffArgs := make([]string, 0, len(diffArgs)+2)
	numstatDiffArgs = append(numstatDiffArgs, "diff", "--numstat")
	numstatDiffArgs = append(numstatDiffArgs, diffArgs...)
	output, err := runGoalDiffGit(gitCtx, projectRootPath, numstatDiffArgs...)
	if err != nil {
		response.Reason = goalDiffFailureReason(gitCtx, "diff_failed")
		writeJSON(w, http.StatusOK, response)
		return
	}
	files, err := parseTaskCommitDiffNumstat(string(output))
	if err != nil {
		response.Reason = "diff_failed"
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.Available = true
	response.Files = files
	response.FilesChanged = len(files)
	for _, file := range files {
		response.Insertions += file.Insertions
		response.Deletions += file.Deletions
	}
	writeJSON(w, http.StatusOK, response)
}

func goalDiffMergeCommitForBranch(ctx context.Context, projectRootPath, branch, baseRevision string) (string, error) {
	branchRef := "refs/heads/" + branch

	tipOutput, err := runGoalDiffGit(ctx, projectRootPath, "rev-parse", branchRef)
	if err != nil {
		return "", err
	}
	branchTip := strings.TrimSpace(string(tipOutput))

	output, err := runGoalDiffGit(ctx, projectRootPath, "rev-list", "--ancestry-path", "--merges", "--topo-order", "--reverse", branchRef+".."+baseRevision)
	if err != nil {
		return "", err
	}
	for _, mergeCommit := range strings.Fields(string(output)) {
		secondParent, err := runGoalDiffGit(ctx, projectRootPath, "rev-parse", mergeCommit+"^2")
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			continue
		}
		if strings.TrimSpace(string(secondParent)) == branchTip {
			return mergeCommit, nil
		}
	}
	return "", nil
}

func goalDiffMergeCommitByMessage(ctx context.Context, projectRootPath, goalID, baseRevision string) (string, int, error) {
	pattern := "^Merge goal " + goalID + "([: ]|$)"
	output, err := runGoalDiffGit(ctx, projectRootPath, "log", "-E", "--grep="+pattern, "--format=%H %P", baseRevision)
	if err != nil {
		return "", 0, err
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		return fields[0], len(fields) - 1, nil
	}
	return "", 0, nil
}

func goalWorktreeBranch(goalID int64) string {
	goal8 := strconv.FormatInt(goalID, 10)
	if len(goal8) > 8 {
		goal8 = goal8[:8]
	}
	return "wt/goal-" + goal8
}

func resolveGoalDiffBase(ctx context.Context, projectRootPath string) (string, string, error) {
	output, err := runGoalDiffGit(ctx, projectRootPath, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		if name := goalDiffRemoteHeadName(string(output)); name != "" {
			return goalDiffBaseRevision(ctx, projectRootPath, name)
		}
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	for _, name := range []string{"main", "master"} {
		baseRef, baseRevision, err := goalDiffBaseRevision(ctx, projectRootPath, name)
		if err == nil {
			return baseRef, baseRevision, nil
		}
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
	}
	return "", "", errNoGoalDiffBase
}

func goalDiffRemoteHeadName(output string) string {
	value := strings.TrimSpace(output)
	const remotePrefix = "origin/"
	if strings.HasPrefix(value, remotePrefix) {
		return strings.TrimPrefix(value, remotePrefix)
	}
	if _, name, ok := strings.Cut(value, "/"); ok {
		return name
	}
	return ""
}

func goalDiffBaseRevision(ctx context.Context, projectRootPath, name string) (string, string, error) {
	localRef := "refs/heads/" + name
	if _, err := runGoalDiffGit(ctx, projectRootPath, "show-ref", "--verify", "--quiet", localRef); err == nil {
		return name, localRef, nil
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	remoteRef := "refs/remotes/origin/" + name
	if _, err := runGoalDiffGit(ctx, projectRootPath, "show-ref", "--verify", "--quiet", remoteRef); err == nil {
		return name, remoteRef, nil
	} else if ctx.Err() != nil {
		return "", "", ctx.Err()
	}
	return "", "", errNoGoalDiffBase
}

func runGoalDiffGit(ctx context.Context, projectRootPath string, args ...string) ([]byte, error) {
	gitArgs := []string{"-c", "core.quotepath=false", "-C", projectRootPath}
	gitArgs = append(gitArgs, args...)
	return exec.CommandContext(ctx, "git", gitArgs...).Output()
}

func goalDiffFailureReason(ctx context.Context, fallback string) string {
	if ctx.Err() != nil {
		return "timeout"
	}
	return fallback
}

func goalDiffLineCount(output string) int {
	if output == "" {
		return 0
	}
	count := strings.Count(output, "\n")
	if !strings.HasSuffix(output, "\n") {
		count++
	}
	return count
}

var errNoGoalDiffBase = &goalDiffBaseError{}

type goalDiffBaseError struct{}

func (*goalDiffBaseError) Error() string {
	return "no base branch"
}
