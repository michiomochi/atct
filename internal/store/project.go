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
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

var (
	ErrProjectNotFound       = errors.New("project not found for cwd")
	ErrProjectAlreadyClaimed = errors.New("project already claimed")
)

func (s *Store) CreateProject(ctx context.Context, name, rootPath string) (domain.Project, error) {
	rootPath = normalizeProjectPath(rootPath)
	ns := domain.Project{
		ID:        uuid.NewString(),
		Name:      name,
		RootPath:  rootPath,
		CreatedAt: time.Now().UTC(),
	}
	err := sqlcgen.New(s.db).CreateProject(ctx, sqlcgen.CreateProjectParams{
		ID:        ns.ID,
		Name:      ns.Name,
		RootPath:  ns.RootPath,
		CreatedAt: ns.CreatedAt.Format(time.RFC3339),
	})
	if err != nil {
		return domain.Project{}, fmt.Errorf("insert project: %w", err)
	}
	return ns, nil
}

func (s *Store) ClaimProject(ctx context.Context, projectID, agentSessionID string) (domain.Project, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return domain.Project{}, fmt.Errorf("%w: empty id", ErrProjectNotFound)
	}
	agentSessionID = strings.TrimSpace(agentSessionID)

	currentRow, err := sqlcgen.New(s.db).GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
		}
		return domain.Project{}, fmt.Errorf("lookup project claim: %w", err)
	}
	currentClaim := strings.TrimSpace(currentRow.ClaimedBy)
	if agentSessionID != "" && currentClaim != "" && currentClaim != agentSessionID && claimIsRunning(ctx, s, currentClaim) {
		return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectAlreadyClaimed, projectID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Project{}, fmt.Errorf("begin project claim tx: %w", err)
	}
	defer tx.Rollback()

	q := sqlcgen.New(tx)
	project, err := q.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
		}
		return domain.Project{}, fmt.Errorf("lookup project claim: %w", err)
	}
	if agentSessionID != "" && strings.TrimSpace(project.ClaimedBy) != currentClaim && strings.TrimSpace(project.ClaimedBy) != "" {
		return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectAlreadyClaimed, projectID)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	claimedAt := sql.NullString{}
	if agentSessionID != "" {
		claimedAt = sql.NullString{String: now, Valid: true}
	}
	result, err := q.ClaimProject(ctx, sqlcgen.ClaimProjectParams{
		ClaimedBy: agentSessionID,
		ClaimedAt: claimedAt,
		ID:        projectID,
	})
	if err != nil {
		return domain.Project{}, fmt.Errorf("claim project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return domain.Project{}, fmt.Errorf("check claimed project: %w", err)
	}
	if affected == 0 {
		return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	if err := tx.Commit(); err != nil {
		return domain.Project{}, fmt.Errorf("commit project claim: %w", err)
	}

	claimedRow, err := sqlcgen.New(s.db).GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
		}
		return domain.Project{}, fmt.Errorf("lookup claimed project: %w", err)
	}
	return projectFromRow(claimedRow)
}

// ReleaseProject clears a project's claim.
func (s *Store) ReleaseProject(ctx context.Context, projectID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return fmt.Errorf("%w: empty id", ErrProjectNotFound)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin project claim release tx: %w", err)
	}
	defer tx.Rollback()

	result, err := sqlcgen.New(tx).ReleaseProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("release project claim: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check released project: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrProjectNotFound, projectID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit project claim release: %w", err)
	}
	return nil
}

// NormalizeRoot exposes the shared project-root normalization used when
// resolving the current working directory.
func NormalizeRoot(ctx context.Context, path string) string {
	return normalizeWorktreePath(ctx, path, "git")
}

func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := sqlcgen.New(s.db).ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}

	out := []domain.Project{}
	for _, row := range rows {
		p, err := projectFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ResolveProject maps a worktree to its main repository before selecting the
// longest matching root_path. This prevents a worktree from becoming a second
// project for the same repository.
func (s *Store) ResolveProject(ctx context.Context, cwd string) (domain.Project, error) {
	cwd = normalizeWorktreePath(ctx, cwd, "git")
	cwd = normalizeProjectPath(cwd)
	row, err := sqlcgen.New(s.db).ResolveProject(ctx, sqlcgen.ResolveProjectParams{
		RootPath:   cwd,
		RootPath_2: cwd,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, cwd)
		}
		return domain.Project{}, fmt.Errorf("scan project: %w", err)
	}
	return projectFromRow(row)
}

func projectFromRow(row sqlcgen.Project) (domain.Project, error) {
	t, err := time.Parse(time.RFC3339, row.CreatedAt)
	if err != nil {
		return domain.Project{}, fmt.Errorf("parse created_at: %w", err)
	}
	return domain.Project{
		ID:        row.ID,
		Name:      row.Name,
		RootPath:  row.RootPath,
		CreatedAt: t,
		ClaimedBy: row.ClaimedBy,
		ClaimedAt: parseClaimedAt(row.ClaimedAt),
	}, nil
}

func parseClaimedAt(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, value.String)
	if err != nil {
		return nil
	}
	return &t
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
