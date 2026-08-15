package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/domain"
)

var ErrProjectNotFound = errors.New("project not found for cwd")

func (s *Store) CreateProject(ctx context.Context, name, rootPath string) (domain.Project, error) {
	rootPath = normalizeProjectPath(rootPath)
	ns := domain.Project{
		ID:        uuid.NewString(),
		Name:      name,
		RootPath:  rootPath,
		CreatedAt: time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects (id, name, root_path, created_at) VALUES (?, ?, ?, ?)
	`, ns.ID, ns.Name, ns.RootPath, ns.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.Project{}, fmt.Errorf("insert project: %w", err)
	}
	return ns, nil
}

// NormalizeRoot exposes the shared project-root normalization used when
// resolving the current working directory.
func NormalizeRoot(ctx context.Context, path string) string {
	return normalizeWorktreePath(ctx, path, "git")
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, root_path, created_at FROM projects ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		var createdAt string
		if err := rows.Scan(&p.ID, &p.Name, &p.RootPath, &createdAt); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		p.CreatedAt = t
		out = append(out, p)
	}
	return out, rows.Err()
}

// ResolveProject maps a worktree to its main repository before selecting the
// longest matching root_path. This prevents a worktree from becoming a second
// project for the same repository.
func (s *Store) ResolveProject(ctx context.Context, cwd string) (domain.Project, error) {
	cwd = normalizeWorktreePath(ctx, cwd, "git")
	cwd = normalizeProjectPath(cwd)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, created_at FROM projects
		WHERE ? = root_path OR ? LIKE root_path || '/%'
		ORDER BY LENGTH(root_path) DESC LIMIT 1
	`, cwd, cwd)

	var ns domain.Project
	var createdAt string
	if err := row.Scan(&ns.ID, &ns.Name, &ns.RootPath, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, cwd)
		}
		return domain.Project{}, fmt.Errorf("scan project: %w", err)
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return domain.Project{}, fmt.Errorf("parse created_at: %w", err)
	}
	ns.CreatedAt = t
	return ns, nil
}

func normalizeWorktreePath(ctx context.Context, cwd, gitCommand string) string {
	topLevelCmd := exec.CommandContext(ctx, gitCommand, "-C", cwd, "rev-parse", "--show-toplevel")
	topLevelOutput, err := topLevelCmd.Output()
	if err != nil {
		return cwd
	}
	repoRoot := strings.TrimSpace(string(topLevelOutput))
	if repoRoot == "" {
		return cwd
	}

	commonDirCmd := exec.CommandContext(ctx, gitCommand, "-C", cwd, "rev-parse", "--git-common-dir")
	commonDirOutput, err := commonDirCmd.Output()
	if err != nil {
		return cwd
	}
	commonDir := strings.TrimSpace(string(commonDirOutput))
	if commonDir == "" {
		return cwd
	}

	gitDirCmd := exec.CommandContext(ctx, gitCommand, "-C", cwd, "rev-parse", "--git-dir")
	gitDirOutput, err := gitDirCmd.Output()
	if err != nil {
		return cwd
	}
	gitDir := strings.TrimSpace(string(gitDirOutput))
	if gitDir == "" {
		return cwd
	}

	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cwd, gitDir)
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(cwd, commonDir)
	}
	gitDir, err = filepath.Abs(gitDir)
	if err != nil {
		return cwd
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil {
		return cwd
	}
	gitDir = normalizeProjectPath(gitDir)
	commonDir = normalizeProjectPath(commonDir)
	if filepath.Clean(gitDir) == filepath.Clean(commonDir) {
		return cwd
	}
	if filepath.Base(commonDir) != ".git" {
		return cwd
	}
	return filepath.Dir(commonDir)
}

func normalizeProjectPath(path string) string {
	path = strings.TrimRight(path, "/")
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path
}
