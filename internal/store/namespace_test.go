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

func TestResolveNamespacePrefersLongestMatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.CreateNamespace(ctx, "outer", "/repos"); err != nil {
		t.Fatalf("CreateNamespace outer: %v", err)
	}
	if _, err := s.CreateNamespace(ctx, "inner", "/repos/atct"); err != nil {
		t.Fatalf("CreateNamespace inner: %v", err)
	}

	got, err := s.ResolveNamespace(ctx, "/repos/atct/internal/store")
	if err != nil {
		t.Fatalf("ResolveNamespace: %v", err)
	}
	if got.Name != "inner" {
		t.Fatalf("got %q, want %q", got.Name, "inner")
	}
}

func TestResolveNamespaceErrorsWhenNoMatch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.ResolveNamespace(ctx, "/somewhere/else"); err == nil {
		t.Fatal("expected error when no namespace matches, got nil")
	}
}

func TestResolveNamespaceMapsWorktreeToMainRepository(t *testing.T) {
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
	if _, err := s.CreateNamespace(ctx, "main", mainRepo); err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}
	got, err := s.ResolveNamespace(ctx, nested)
	if err != nil {
		t.Fatalf("ResolveNamespace: %v", err)
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
