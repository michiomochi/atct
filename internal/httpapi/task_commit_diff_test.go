package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

type taskCommitDiffResponse struct {
	SHA          string               `json:"sha"`
	InHistory    bool                 `json:"in_history"`
	Files        []taskCommitDiffFile `json:"files"`
	Body         string               `json:"body"`
	OmittedLines int                  `json:"omitted_lines"`
}

type taskCommitDiffFile struct {
	Path       string `json:"path"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary"`
}

func TestHTTPTaskCommitDiffRejectsUnlinkedCommit(t *testing.T) {
	f := newBareFixture(t)
	task := declareTask(t, f)
	linkedSHA := createGitCommit(t, f.project.RootPath, map[string]string{"linked.txt": "linked\n"})
	linkTaskCommit(t, f, task.ID, linkedSHA)

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, fmt.Sprintf("%s/api/tasks/%d/commits/%s/diff", srv.URL, task.ID, strings.Repeat("a", 40)), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusNotFound, body)
	}
}

func TestHTTPTaskCommitDiffRejectsMissingTask(t *testing.T) {
	f := newBareFixture(t)

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, fmt.Sprintf("%s/api/tasks/%s/commits/%s/diff", srv.URL, "missing-task", strings.Repeat("a", 40)), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusNotFound, body)
	}
}

func TestHTTPTaskCommitDiffReturnsEmptyFilesForCommitOutsideHistory(t *testing.T) {
	f := newBareFixture(t)
	task := declareTask(t, f)
	sha := strings.Repeat("b", 40)
	linkTaskCommit(t, f, task.ID, sha)

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, fmt.Sprintf("%s/api/tasks/%d/commits/%s/diff", srv.URL, task.ID, sha), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	var response taskCommitDiffResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	if response.InHistory {
		t.Fatal("in_history = true, want false")
	}
	if response.Files == nil {
		t.Fatal("files = null, want an empty array")
	}
	if len(response.Files) != 0 {
		t.Fatalf("len(files) = %d, want 0", len(response.Files))
	}
	if response.Body != "" {
		t.Fatalf("body = %q, want empty", response.Body)
	}
	if response.OmittedLines != 0 {
		t.Fatalf("omitted_lines = %d, want 0", response.OmittedLines)
	}
}

func TestHTTPTaskCommitDiffOmitsBodyLinesButKeepsAllFiles(t *testing.T) {
	f := newBareFixture(t)
	task := declareTask(t, f)
	sha := createGitCommit(t, f.project.RootPath, map[string]string{
		"large.txt": strings.Repeat("large\n", 500),
		"small.txt": "small\n",
	})
	linkTaskCommit(t, f, task.ID, sha)

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, fmt.Sprintf("%s/api/tasks/%d/commits/%s/diff", srv.URL, task.ID, sha), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	var response taskCommitDiffResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	if response.OmittedLines <= 0 {
		t.Fatalf("omitted_lines = %d, want > 0", response.OmittedLines)
	}
	if response.Files == nil {
		t.Fatal("files = null, want all files")
	}
	paths := make(map[string]bool, len(response.Files))
	for _, file := range response.Files {
		paths[file.Path] = true
	}
	for _, path := range []string{"large.txt", "small.txt"} {
		if !paths[path] {
			t.Fatalf("files do not include %q: %#v", path, response.Files)
		}
	}
}

func TestHTTPTaskCommitDiffKeepsBodyWithinLimit(t *testing.T) {
	f := newBareFixture(t)
	task := declareTask(t, f)
	sha := createGitCommit(t, f.project.RootPath, map[string]string{"small.txt": "one\ntwo\nthree\n"})
	linkTaskCommit(t, f, task.ID, sha)

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, fmt.Sprintf("%s/api/tasks/%d/commits/%s/diff", srv.URL, task.ID, sha), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	var response taskCommitDiffResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, body)
	}
	if response.OmittedLines != 0 {
		t.Fatalf("omitted_lines = %d, want 0", response.OmittedLines)
	}
	if len(response.Files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(response.Files))
	}
}

func declareTask(t *testing.T, f *fixture) domain.Task {
	t.Helper()
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "diff-fixture-agent", "diff-fixture-session", []string{"task"}, []string{"Prepare a commit for the diff endpoint."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("len(tasks) = %d, want 1", len(tasks))
	}
	return tasks[0]
}

func linkTaskCommit(t *testing.T, f *fixture, taskID int64, sha string) {
	t.Helper()
	if err := f.store.LinkTaskCommit(f.ctx, taskID, domain.TaskCommit{SHA: sha, Subject: "diff fixture commit"}); err != nil {
		t.Fatalf("LinkTaskCommit: %v", err)
	}
}

func createGitCommit(t *testing.T, root string, files map[string]string) string {
	t.Helper()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "fixture@example.com")
	runGit(t, root, "config", "user.name", "HTTP API fixture")
	for path, contents := range files {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixture commit")
	return strings.TrimSpace(string(runGit(t, root, "rev-parse", "HEAD")))
}

func runGit(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(context.Background(), "git", append([]string{"-C", root}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}
