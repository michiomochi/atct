package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestResolveProjectPrefersLongestMatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.CreateProject(ctx, "outer", "/repos"); err != nil {
		t.Fatalf("CreateProject outer: %v", err)
	}
	if _, err := s.CreateProject(ctx, "inner", "/repos/atct"); err != nil {
		t.Fatalf("CreateProject inner: %v", err)
	}

	got, err := s.ResolveProject(ctx, "/repos/atct/internal/store")
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got.Name != "inner" {
		t.Fatalf("got %q, want %q", got.Name, "inner")
	}
}

func TestResolveProjectErrorsWhenNoMatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.ResolveProject(ctx, "/somewhere/else"); err == nil {
		t.Fatal("expected error when no project matches, got nil")
	}
}

func TestResolveProjectNormalRepositorySubdirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "store"), 0o755); err != nil {
		t.Fatalf("MkdirAll repository subdirectory: %v", err)
	}
	runTestGit(t, repo, "init", "-q")

	s := newTestStore(t)
	if _, err := s.CreateProject(ctx, "repo", repo); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.ResolveProject(ctx, filepath.Join(repo, "internal", "store"))
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got.Name != "repo" {
		t.Fatalf("got %q, want %q", got.Name, "repo")
	}
}

func TestResolveProjectSeparatesProjectsInsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	alpha := filepath.Join(repo, "alpha")
	beta := filepath.Join(repo, "beta")
	if err := os.MkdirAll(filepath.Join(alpha, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll alpha: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(beta, "src"), 0o755); err != nil {
		t.Fatalf("MkdirAll beta: %v", err)
	}
	runTestGit(t, repo, "init", "-q")

	s := newTestStore(t)
	if _, err := s.CreateProject(ctx, "alpha", alpha); err != nil {
		t.Fatalf("CreateProject alpha: %v", err)
	}
	if _, err := s.CreateProject(ctx, "beta", beta); err != nil {
		t.Fatalf("CreateProject beta: %v", err)
	}

	gotAlpha, err := s.ResolveProject(ctx, filepath.Join(alpha, "src"))
	if err != nil {
		t.Fatalf("ResolveProject alpha: %v", err)
	}
	if gotAlpha.Name != "alpha" {
		t.Fatalf("alpha got %q, want %q", gotAlpha.Name, "alpha")
	}
	gotBeta, err := s.ResolveProject(ctx, filepath.Join(beta, "src"))
	if err != nil {
		t.Fatalf("ResolveProject beta: %v", err)
	}
	if gotBeta.Name != "beta" {
		t.Fatalf("beta got %q, want %q", gotBeta.Name, "beta")
	}
}

func TestResolveProjectMatchesSymlinkedCWD(t *testing.T) {
	ctx := context.Background()
	realRoot := filepath.Join(t.TempDir(), "real")
	symlinkRoot := filepath.Join(t.TempDir(), "symlink")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll real root: %v", err)
	}
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	s := newTestStore(t)
	if _, err := s.CreateProject(ctx, "real", realRoot); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.ResolveProject(ctx, symlinkRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got.Name != "real" {
		t.Fatalf("got %q, want %q", got.Name, "real")
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestResolveProjectMapsWorktreeToMainRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}

	ctx := context.Background()
	mainRepo := filepath.Join(t.TempDir(), "main")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("MkdirAll main repo: %v", err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	runGit(mainRepo, "init", "-q")
	if err := os.WriteFile(filepath.Join(mainRepo, "README"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(mainRepo, "add", "README")
	runGit(mainRepo, "-c", "user.name=Test", "-c", "user.email=test@example.com",
		"commit", "-qm", "initial")
	runGit(mainRepo, "worktree", "add", "-q", worktree, "HEAD")
	nested := filepath.Join(worktree, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested worktree path: %v", err)
	}

	s := newTestStore(t)
	if _, err := s.CreateProject(ctx, "main", mainRepo); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	got, err := s.ResolveProject(ctx, nested)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got.Name != "main" {
		t.Fatalf("got %q, want %q", got.Name, "main")
	}
}

func TestNormalizeWorktreePathFallsBackWhenGitIsUnavailable(t *testing.T) {
	got := normalizeWorktreePath(context.Background(), "/input/path", "definitely-not-a-real-git-command")
	if got != "/input/path" {
		t.Fatalf("got %q, want the original path", got)
	}
}
