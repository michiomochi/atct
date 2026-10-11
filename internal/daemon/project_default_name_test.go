package daemon

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDaemonRegistersSameRepoNameFromDifferentOrgsWithDefaultNames(t *testing.T) {
	requireDaemonGit(t)
	conn := newDaemonConn(t)

	roots := map[string]string{
		"kanmu":       newDaemonGitRepo(t, "https://github.com/kanmu/atct.git"),
		"michiomochi": newDaemonGitRepo(t, "https://github.com/michiomochi/atct.git"),
	}
	for org, root := range roots {
		resp := call(t, conn, "goal.list", map[string]any{"cwd": root})
		if resp.Error != "" {
			t.Fatalf("goal.list for %s root: %v", org, resp.Error)
		}
		var result struct {
			Project domain.Project `json:"project"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("unmarshal goal.list: %v", err)
		}
		if want := org + "/atct"; result.Project.Name != want {
			t.Fatalf("project name = %q, want %q", result.Project.Name, want)
		}
	}

	listed := call(t, conn, "project.list", map[string]any{})
	if listed.Error != "" {
		t.Fatalf("project.list: %v", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("project.list returned %d projects, want 2: %#v", len(projects), projects)
	}
}

func TestDaemonProjectCreateNameOverridesDefault(t *testing.T) {
	requireDaemonGit(t)
	root := newDaemonGitRepo(t, "https://github.com/kanmu/atct.git")

	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{
			name:   "explicit name wins over the default",
			params: map[string]any{"name": "explicit", "root_path": root},
			want:   "explicit",
		},
		{
			name:   "empty name uses the default",
			params: map[string]any{"name": "", "root_path": root},
			want:   "kanmu/atct",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := newDaemonConn(t)
			resp := call(t, conn, "project.create", tt.params)
			if resp.Error != "" {
				t.Fatalf("project.create: %v", resp.Error)
			}
			var project domain.Project
			if err := json.Unmarshal(resp.Result, &project); err != nil {
				t.Fatalf("unmarshal project: %v", err)
			}
			if project.Name != tt.want {
				t.Fatalf("project name = %q, want %q", project.Name, tt.want)
			}
		})
	}
}

func TestDaemonProjectCreateRetriesDefaultNameWithParentBase(t *testing.T) {
	requireDaemonGit(t)
	first := newDaemonGitRepo(t, "")
	second := newDaemonGitRepo(t, "")
	conn := newDaemonConn(t)

	tests := []struct {
		root string
		want string
	}{
		{root: first, want: "atct"},
		{root: second, want: filepath.Base(filepath.Dir(second)) + "/atct"},
	}
	for _, tt := range tests {
		resp := call(t, conn, "project.create", map[string]any{"root_path": tt.root})
		if resp.Error != "" {
			t.Fatalf("project.create %s: %v", tt.root, resp.Error)
		}
		var project domain.Project
		if err := json.Unmarshal(resp.Result, &project); err != nil {
			t.Fatalf("unmarshal project: %v", err)
		}
		if project.Name != tt.want {
			t.Fatalf("project name = %q, want %q", project.Name, tt.want)
		}
	}
}

func TestDaemonProjectCreateExplicitDuplicateNameIsNotRetried(t *testing.T) {
	requireDaemonGit(t)
	first := newDaemonGitRepo(t, "")
	second := newDaemonGitRepo(t, "")
	conn := newDaemonConn(t)

	resp := call(t, conn, "project.create", map[string]any{"name": "dup", "root_path": first})
	if resp.Error != "" {
		t.Fatalf("first project.create: %v", resp.Error)
	}
	resp = call(t, conn, "project.create", map[string]any{"name": "dup", "root_path": second})
	if resp.Error == "" {
		t.Fatal("duplicate explicit name succeeded, want error")
	}
	if !strings.Contains(resp.Error, "give a different name") {
		t.Fatalf("error = %q, want it to ask for a different name", resp.Error)
	}

	listed := call(t, conn, "project.list", map[string]any{})
	if listed.Error != "" {
		t.Fatalf("project.list: %v", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project.list returned %d projects, want 1: %#v", len(projects), projects)
	}
}

// newDaemonGitRepo creates a git repository named "atct" under a fresh temp dir
// and returns its path. An empty remote skips the origin. The daemon tests
// cannot reach store's test helpers.
func newDaemonGitRepo(t *testing.T, remote string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "atct")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("MkdirAll repo: %v", err)
	}
	argsList := [][]string{{"init", "-q"}}
	if remote != "" {
		argsList = append(argsList, []string{"remote", "add", "origin", remote})
	}
	for _, args := range argsList {
		if output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repo
}

func requireDaemonGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
}
