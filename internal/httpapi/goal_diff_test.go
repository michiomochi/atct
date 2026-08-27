package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type goalDiffResponse struct {
	Available    bool                 `json:"available"`
	Reason       string               `json:"reason"`
	BaseRef      string               `json:"base_ref"`
	Branch       string               `json:"branch"`
	FilesChanged int                  `json:"files_changed"`
	Insertions   int                  `json:"insertions"`
	Deletions    int                  `json:"deletions"`
	Files        []taskCommitDiffFile `json:"files"`
	Path         string               `json:"path"`
	Patch        string               `json:"patch"`
	OmittedLines int                  `json:"omitted_lines"`
}

func TestHTTPGoalDiffReturnsNumstatForGoalBranch(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "main")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{
		"changed.txt": "before\n",
	})
	branch := createGoalDiffBranch(t, f.project.RootPath, f.goal.ID)
	commitGoalDiffFiles(t, f.project.RootPath, "goal", map[string]string{
		"added.txt":   "one\ntwo\n",
		"changed.txt": "after\nnew\n",
	})
	setGoalDiffRemoteHead(t, f.project.RootPath, "main")

	response := requestGoalDiff(t, f, "")
	if !response.Available || response.Reason != "" {
		t.Fatalf("available/reason = %v/%q, want true/empty; response=%+v", response.Available, response.Reason, response)
	}
	if response.BaseRef != "main" || response.Branch != branch {
		t.Fatalf("refs = %q/%q, want main/%q", response.BaseRef, response.Branch, branch)
	}
	if response.FilesChanged != 2 || response.Insertions != 4 || response.Deletions != 1 {
		t.Fatalf("summary = files %d, insertions %d, deletions %d; want 2, 4, 1", response.FilesChanged, response.Insertions, response.Deletions)
	}
	files := make(map[string]taskCommitDiffFile, len(response.Files))
	for _, file := range response.Files {
		files[file.Path] = file
	}
	if got := files["added.txt"]; got.Insertions != 2 || got.Deletions != 0 || got.Binary {
		t.Fatalf("added.txt = %+v, want 2 insertions, 0 deletions, text", got)
	}
	if got := files["changed.txt"]; got.Insertions != 2 || got.Deletions != 1 || got.Binary {
		t.Fatalf("changed.txt = %+v, want 2 insertions, 1 deletion, text", got)
	}
}

func TestHTTPGoalDiffReturnsRawPatchForPath(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "main")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{"base.txt": "base\n"})
	createGoalDiffBranch(t, f.project.RootPath, f.goal.ID)
	commitGoalDiffFiles(t, f.project.RootPath, "goal", map[string]string{"patch.txt": "one\ntwo\n"})
	setGoalDiffRemoteHead(t, f.project.RootPath, "main")

	response := requestGoalDiff(t, f, "patch.txt")
	if !response.Available || response.Reason != "" {
		t.Fatalf("available/reason = %v/%q, want true/empty; response=%+v", response.Available, response.Reason, response)
	}
	if response.Path != "patch.txt" {
		t.Fatalf("path = %q, want patch.txt", response.Path)
	}
	if !strings.HasPrefix(response.Patch, "diff --git ") {
		t.Fatalf("patch does not start with diff --git: %q", response.Patch)
	}
	if response.OmittedLines != 0 {
		t.Fatalf("omitted_lines = %d, want 0", response.OmittedLines)
	}
}

func TestHTTPGoalDiffResolvesMasterAsDefaultBranch(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "master")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{"base.txt": "base\n"})
	createGoalDiffBranch(t, f.project.RootPath, f.goal.ID)
	commitGoalDiffFiles(t, f.project.RootPath, "goal", map[string]string{"goal.txt": "goal\n"})
	setGoalDiffRemoteHead(t, f.project.RootPath, "master")

	response := requestGoalDiff(t, f, "")
	if !response.Available || response.Reason != "" {
		t.Fatalf("available/reason = %v/%q, want true/empty; response=%+v", response.Available, response.Reason, response)
	}
	if response.BaseRef != "master" {
		t.Fatalf("base_ref = %q, want master", response.BaseRef)
	}
}

func TestHTTPGoalDiffReturnsUnavailableForMissingGoalBranch(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "main")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{"base.txt": "base\n"})
	setGoalDiffRemoteHead(t, f.project.RootPath, "main")

	response := requestGoalDiff(t, f, "")
	if response.Available || response.Reason != "no_branch" {
		t.Fatalf("available/reason = %v/%q, want false/no_branch; response=%+v", response.Available, response.Reason, response)
	}
	if response.Files == nil || len(response.Files) != 0 {
		t.Fatalf("files = %#v, want an empty array", response.Files)
	}
	if response.Patch != "" {
		t.Fatalf("patch = %q, want empty", response.Patch)
	}
}

func TestHTTPGoalDiffExcludesDefaultBranchOnlyChangesAfterMerge(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "main")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{"base.txt": "base\n"})
	branch := createGoalDiffBranch(t, f.project.RootPath, f.goal.ID)
	commitGoalDiffFiles(t, f.project.RootPath, "goal", map[string]string{"goal.txt": "goal\n"})
	runGit(t, f.project.RootPath, "switch", "main")
	commitGoalDiffFiles(t, f.project.RootPath, "main before merge", map[string]string{"merged-main.txt": "merged\n"})
	runGit(t, f.project.RootPath, "switch", branch)
	runGit(t, f.project.RootPath, "merge", "--no-edit", "main")
	runGit(t, f.project.RootPath, "switch", "main")
	commitGoalDiffFiles(t, f.project.RootPath, "main after merge", map[string]string{"main-after-merge.txt": "main only\n"})
	setGoalDiffRemoteHead(t, f.project.RootPath, "main")

	response := requestGoalDiff(t, f, "")
	paths := make(map[string]bool, len(response.Files))
	for _, file := range response.Files {
		paths[file.Path] = true
	}
	if paths["main-after-merge.txt"] {
		t.Fatalf("default-branch-only file is included: %#v", response.Files)
	}
	if !paths["goal.txt"] {
		t.Fatalf("goal file is missing: %#v", response.Files)
	}
}

func TestHTTPGoalDiffReturnsNotGitForNonRepository(t *testing.T) {
	f := newBareFixture(t)

	response := requestGoalDiff(t, f, "")
	if response.Available || response.Reason != "not_git" {
		t.Fatalf("available/reason = %v/%q, want false/not_git; response=%+v", response.Available, response.Reason, response)
	}
}

func TestHTTPGoalDiffReturnsEmptyPatchForUnknownPath(t *testing.T) {
	f := newBareFixture(t)
	initGoalDiffRepository(t, f.project.RootPath, "main")
	commitGoalDiffFiles(t, f.project.RootPath, "base", map[string]string{"base.txt": "base\n"})
	createGoalDiffBranch(t, f.project.RootPath, f.goal.ID)
	commitGoalDiffFiles(t, f.project.RootPath, "goal", map[string]string{"known.txt": "known\n"})
	setGoalDiffRemoteHead(t, f.project.RootPath, "main")

	response := requestGoalDiff(t, f, "missing.txt")
	if !response.Available || response.Reason != "" {
		t.Fatalf("available/reason = %v/%q, want true/empty; response=%+v", response.Available, response.Reason, response)
	}
	if response.Path != "missing.txt" {
		t.Fatalf("path = %q, want missing.txt", response.Path)
	}
	if response.Patch != "" {
		t.Fatalf("patch = %q, want empty", response.Patch)
	}
}

func requestGoalDiff(t *testing.T, f *fixture, path string) goalDiffResponse {
	t.Helper()
	srv := newTestServer(t, f.store)
	defer srv.Close()
	endpoint := fmt.Sprintf("%s/api/goals/%d/diff", srv.URL, f.goal.ID)
	if path != "" {
		endpoint += "?path=" + path
	}
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, endpoint, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	var response goalDiffResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	return response
}

func initGoalDiffRepository(t *testing.T, root, defaultBranch string) {
	t.Helper()
	runGit(t, root, "init", "-b", defaultBranch)
	runGit(t, root, "config", "user.email", "fixture@example.com")
	runGit(t, root, "config", "user.name", "HTTP API fixture")
}

func commitGoalDiffFiles(t *testing.T, root, message string, files map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(files))
	for path, contents := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	runGit(t, root, append([]string{"add", "--"}, paths...)...)
	runGit(t, root, "commit", "-m", message)
}

func createGoalDiffBranch(t *testing.T, root string, goalID int64) string {
	t.Helper()
	branch := fmt.Sprintf("wt/goal-%d", goalID)
	runGit(t, root, "switch", "-c", branch)
	return branch
}

func setGoalDiffRemoteHead(t *testing.T, root, defaultBranch string) {
	t.Helper()
	runGit(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+defaultBranch)
}
