package httpapi_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Runs against a copy of a real repository when ATCT_REAL_REPO points at one.
// The expected merge state uses main's second-parent set rather than the
// implementation's ancestry path, so incorrect merge attribution is detected.
func TestHTTPGoalDiffAgainstRealRepository(t *testing.T) {
	realRepo := os.Getenv("ATCT_REAL_REPO")
	if realRepo == "" {
		t.Skip("set ATCT_REAL_REPO to a real repository")
	}

	f := newBareFixture(t)
	cloneOutput, err := exec.Command("git", "clone", "--quiet", "--shared", realRepo, f.project.RootPath).CombinedOutput()
	if err != nil {
		t.Fatalf("clone: %v: %s", err, cloneOutput)
	}
	if err := exec.Command("git", "-C", f.project.RootPath, "show-ref", "--verify", "--quiet", "refs/heads/main").Run(); err != nil {
		if output, err := exec.Command("git", "-C", f.project.RootPath, "branch", "main", "refs/remotes/origin/main").CombinedOutput(); err != nil {
			t.Fatalf("create main branch: %v: %s", err, output)
		}
	}
	if output, err := exec.Command("git", "-C", f.project.RootPath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main").CombinedOutput(); err != nil {
		t.Fatalf("set origin/HEAD: %v: %s", err, output)
	}

	mergeOutput, err := exec.Command("git", "-C", f.project.RootPath, "rev-list", "--merges", "--parents", "refs/heads/main").Output()
	if err != nil {
		t.Fatalf("rev-list merges: %v", err)
	}
	secondParents := make(map[string]struct{})
	for _, line := range strings.Split(string(mergeOutput), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		for _, parent := range fields[2:] {
			secondParents[parent] = struct{}{}
		}
	}

	listOutput, err := exec.Command("git", "-C", f.project.RootPath, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/wt/goal-*").Output()
	if err != nil {
		t.Fatalf("for-each-ref: %v", err)
	}
	refs := strings.Fields(string(listOutput))
	if len(refs) == 0 {
		t.Fatal("no refs/remotes/origin/wt/goal-* refs found")
	}

	for _, ref := range refs {
		name := strings.TrimPrefix(ref, "origin/")
		if !strings.HasPrefix(name, "wt/goal-") {
			continue
		}
		branch := fmt.Sprintf("wt/goal-%d", f.goal.ID)
		if output, err := exec.Command("git", "-C", f.project.RootPath, "branch", "-f", branch, ref).CombinedOutput(); err != nil {
			t.Fatalf("branch -f %s %s: %v: %s", branch, ref, err, output)
		}

		tipOutput, err := exec.Command("git", "-C", f.project.RootPath, "rev-parse", "refs/heads/"+branch).Output()
		if err != nil {
			t.Fatalf("rev-parse %s: %v", branch, err)
		}
		_, expectedMerged := secondParents[strings.TrimSpace(string(tipOutput))]

		response := requestGoalDiff(t, f, "")
		if response.Reason != "" || !response.Available {
			t.Errorf("branch %s: expected available=true and reason=empty, got available=%v reason=%q", name, response.Available, response.Reason)
			continue
		}
		if expectedMerged {
			if response.Source != "merge_commit" || len(response.MergeCommit) != 40 || response.FilesChanged <= 0 {
				t.Errorf("branch %s: expected merged available=true source=merge_commit merge_commit=40-chars files_changed>0, got available=%v source=%q merge_commit=%q files_changed=%d reason=%q", name, response.Available, response.Source, response.MergeCommit, response.FilesChanged, response.Reason)
			}
			continue
		}
		if response.Source != "branch" || response.MergeCommit != "" {
			t.Errorf("branch %s: expected unmerged available=true source=branch merge_commit=empty, got available=%v source=%q merge_commit=%q files_changed=%d reason=%q", name, response.Available, response.Source, response.MergeCommit, response.FilesChanged, response.Reason)
		}
	}
}
