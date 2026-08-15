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

var ErrNamespaceNotFound = errors.New("namespace not found for cwd")

func (s *Store) CreateNamespace(ctx context.Context, name, rootPath string) (domain.Namespace, error) {
	rootPath = strings.TrimRight(rootPath, "/")
	if resolved, err := filepath.EvalSymlinks(rootPath); err == nil {
		rootPath = resolved
	}
	ns := domain.Namespace{
		ID:        uuid.NewString(),
		Name:      name,
		RootPath:  rootPath,
		CreatedAt: time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO namespaces (id, name, root_path, created_at) VALUES (?, ?, ?, ?)
	`, ns.ID, ns.Name, ns.RootPath, ns.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.Namespace{}, fmt.Errorf("insert namespace: %w", err)
	}
	return ns, nil
}

// ResolveNamespace maps a worktree to its main repository before selecting the
// longest matching root_path. This prevents a worktree from becoming a second
// namespace for the same repository.
func (s *Store) ResolveNamespace(ctx context.Context, cwd string) (domain.Namespace, error) {
	cwd = normalizeWorktreePath(ctx, cwd, "git")
	cwd = strings.TrimRight(cwd, "/")
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, root_path, created_at FROM namespaces
		WHERE ? = root_path OR ? LIKE root_path || '/%'
		ORDER BY LENGTH(root_path) DESC LIMIT 1
	`, cwd, cwd)

	var ns domain.Namespace
	var createdAt string
	if err := row.Scan(&ns.ID, &ns.Name, &ns.RootPath, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Namespace{}, fmt.Errorf("%w: %s", ErrNamespaceNotFound, cwd)
		}
		return domain.Namespace{}, fmt.Errorf("scan namespace: %w", err)
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return domain.Namespace{}, fmt.Errorf("parse created_at: %w", err)
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
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repoRoot, commonDir)
	}
	commonDir, err = filepath.Abs(commonDir)
	if err != nil || filepath.Base(commonDir) != ".git" {
		return cwd
	}
	return filepath.Dir(commonDir)
}
