package store

import (
	"context"
	"errors"
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

func TestListProjectsReturnsAllInCreationOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if _, err := s.CreateProject(ctx, "first", "/repos/first"); err != nil {
		t.Fatalf("CreateProject first: %v", err)
	}
	if _, err := s.CreateProject(ctx, "second", "/repos/second"); err != nil {
		t.Fatalf("CreateProject second: %v", err)
	}

	got, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListProjects returned %d projects, want 2", len(got))
	}
	if got[0].Name != "first" || got[1].Name != "second" {
		t.Fatalf("unexpected order: %q then %q", got[0].Name, got[1].Name)
	}
}

func TestListProjectsIsEmptyWhenNoneExist(t *testing.T) {
	got, err := newTestStore(t).ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListProjects returned %d projects, want 0", len(got))
	}
}

func TestNormalizeRootMapsWorktreeToMainRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	ctx := context.Background()

	mainRepo := filepath.Join(t.TempDir(), "main")
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainRepo, "init", "-q")
	if err := os.WriteFile(filepath.Join(mainRepo, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit(mainRepo, "add", "README")
	runGit(mainRepo, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-qm", "init")
	runGit(mainRepo, "worktree", "add", "-q", worktree, "HEAD")

	got := NormalizeRoot(ctx, worktree)
	want, err := filepath.EvalSymlinks(mainRepo)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotResolved != want {
		t.Fatalf("NormalizeRoot = %q, want %q", gotResolved, want)
	}
}

func TestClaimProjectClaimsUnclaimedProject(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "claimable", "/projects/claimable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := s.ClaimProject(ctx, project.ID, testSessionID("session-one"))
	if err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	if got.ClaimedBy != testSessionID("session-one") {
		t.Fatalf("ClaimedBy = %d, want %d", got.ClaimedBy, testSessionID("session-one"))
	}
	if got.ClaimedAt == nil {
		t.Fatal("ClaimedAt is nil, want a claim timestamp")
	}
}

func TestReleaseProjectClearsClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "releasable", "/projects/releasable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, testSessionID("session-one")); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	if err := s.ReleaseProject(ctx, project.ID); err != nil {
		t.Fatalf("ReleaseProject: %v", err)
	}
	got, err := s.ResolveProject(ctx, project.RootPath)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got.ClaimedBy != 0 {
		t.Fatalf("ClaimedBy = %d, want empty", got.ClaimedBy)
	}
	if got.ClaimedAt != nil {
		t.Fatalf("ClaimedAt = %v, want nil", got.ClaimedAt)
	}
}

func TestReleasedProjectCanBeClaimedByAnotherSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "reclaimable", "/projects/reclaimable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, testSessionID("session-one")); err != nil {
		t.Fatalf("first ClaimProject: %v", err)
	}
	if err := s.ReleaseProject(ctx, project.ID); err != nil {
		t.Fatalf("ReleaseProject: %v", err)
	}

	got, err := s.ClaimProject(ctx, project.ID, testSessionID("session-two"))
	if err != nil {
		t.Fatalf("second ClaimProject: %v", err)
	}
	if got.ClaimedBy != testSessionID("session-two") {
		t.Fatalf("ClaimedBy = %d, want %d", got.ClaimedBy, testSessionID("session-two"))
	}
}

func TestClaimProjectRejectsSecondLiveSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "live-claimed", "/projects/live-claimed")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	liveID, err := s.RegisterAgentSession(ctx, os.Getpid())
	if err != nil {
		t.Fatalf("RegisterAgentSession: %v", err)
	}
	if _, err := s.ClaimProject(ctx, project.ID, liveID); err != nil {
		t.Fatalf("first ClaimProject: %v", err)
	}

	_, err = s.ClaimProject(ctx, project.ID, testSessionID("other-session"))
	if !errors.Is(err, ErrProjectAlreadyClaimed) {
		t.Fatalf("second ClaimProject error = %v, want ErrProjectAlreadyClaimed", err)
	}
}

func TestClaimProjectTakesOverDeadSessionClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "dead-claimed", "/projects/dead-claimed")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := s.RegisterAgentSession(ctx, os.Getpid()); err != nil {
		t.Fatalf("RegisterAgentSession live: %v", err)
	}

	deadProcess := exec.Command("sleep", "60")
	if err := deadProcess.Start(); err != nil {
		t.Fatalf("start dead-session fixture: %v", err)
	}
	t.Cleanup(func() {
		if deadProcess.ProcessState == nil {
			_ = deadProcess.Process.Kill()
			_ = deadProcess.Wait()
		}
	})
	deadID, err := s.RegisterAgentSession(ctx, deadProcess.Process.Pid)
	if err != nil {
		t.Fatalf("RegisterAgentSession dead: %v", err)
	}
	if err := deadProcess.Process.Kill(); err != nil {
		t.Fatalf("kill dead-session fixture: %v", err)
	}
	_ = deadProcess.Wait()

	if _, err := s.ClaimProject(ctx, project.ID, deadID); err != nil {
		t.Fatalf("claim with dead session: %v", err)
	}
	got, err := s.ClaimProject(ctx, project.ID, testSessionID("next-session"))
	if err != nil {
		t.Fatalf("take over dead claim: %v", err)
	}
	if got.ClaimedBy != testSessionID("next-session") {
		t.Fatalf("ClaimedBy = %d, want %d", got.ClaimedBy, testSessionID("next-session"))
	}
}

func TestUnclaimedProjectIsReadableByListAndResolve(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "readable", "/projects/readable")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	listed, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("ListProjects returned %d projects, want 1", len(listed))
	}
	if listed[0].ID != project.ID || listed[0].ClaimedBy != 0 || listed[0].ClaimedAt != nil {
		t.Fatalf("listed project = %#v, want unclaimed project %d", listed[0], project.ID)
	}

	resolved, err := s.ResolveProject(ctx, project.RootPath)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if resolved.ID != project.ID || resolved.ClaimedBy != 0 || resolved.ClaimedAt != nil {
		t.Fatalf("resolved project = %#v, want unclaimed project %d", resolved, project.ID)
	}
}
