package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDefaultProjectName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	tests := []struct {
		name   string
		remote string
		subdir string
		// want is "" when the expected result is the base name of the root path.
		want string
	}{
		{name: "https remote with .git", remote: "https://github.com/kanmu/atct.git", want: "kanmu/atct"},
		{name: "https remote without .git", remote: "https://github.com/kanmu/atct", want: "kanmu/atct"},
		{name: "ssh remote with .git", remote: "git@github.com:kanmu/atct.git", want: "kanmu/atct"},
		{name: "ssh remote without .git", remote: "git@github.com:kanmu/atct", want: "kanmu/atct"},
		{name: "ssh:// remote", remote: "ssh://git@github.com/kanmu/atct.git", want: "kanmu/atct"},
		{name: "no remote falls back to base name", remote: "", want: ""},
		{name: "non-GitHub host falls back to base name", remote: "https://gitlab.com/kanmu/atct.git", want: ""},
		{name: "subdirectory is appended", remote: "https://github.com/kanmu/atct.git", subdir: "internal/store", want: "kanmu/atct/internal/store"},
		{name: "subdirectory without remote falls back to base name", remote: "", subdir: "internal/store", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			repo := filepath.Join(t.TempDir(), "repo")
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			runTestGit(t, repo, "init", "-q")
			if tt.remote != "" {
				runTestGit(t, repo, "remote", "add", "origin", tt.remote)
			}

			root := repo
			want := filepath.Base(repo)
			if tt.subdir != "" {
				root = filepath.Join(repo, tt.subdir)
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatalf("MkdirAll subdir: %v", err)
				}
				want = filepath.Base(root)
			}
			if tt.want != "" {
				want = tt.want
			}

			if got := DefaultProjectName(ctx, root); got != want {
				t.Fatalf("DefaultProjectName(%q) = %q, want %q", root, got, want)
			}
		})
	}
}

func TestDefaultProjectNameFallsBackOutsideGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll dir: %v", err)
	}

	if got := DefaultProjectName(context.Background(), dir); got != "plain" {
		t.Fatalf("DefaultProjectName(%q) = %q, want %q", dir, got, "plain")
	}
}
