package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	notify *notifier
}

const schemaVersion = 6

const agentSessionRetention = 30 * 24 * time.Hour

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The daemon is the single writer; limit connections to reduce WAL write contention.
	db.SetMaxOpenConns(1)

	if err := configureDatabase(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrateSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return &Store{db: db, notify: newNotifier()}, nil
}

func requireAgentSessionID(agentSessionID string) (string, error) {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return "", fmt.Errorf("agent_session_id is required")
	}
	return agentSessionID, nil
}

func (s *Store) RegisterAgentSession(ctx context.Context, agentSessionID string, pid int) error {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return err
	}
	storedPID := 0
	startedAt := ""
	if actualStartedAt, err := processStartedAt(pid); err == nil {
		storedPID = pid
		startedAt = actualStartedAt
	}
	now := time.Now().UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent session registration: %w", err)
	}
	defer tx.Rollback()

	queries := sqlcgen.New(s.db).WithTx(tx)
	if err := queries.RegisterAgentSession(ctx, sqlcgen.RegisterAgentSessionParams{
		ID:           agentSessionID,
		Pid:          int64(storedPID),
		StartedAt:    startedAt,
		RegisteredAt: registeredAt,
	}); err != nil {
		return fmt.Errorf("register agent session: %w", err)
	}
	if err := queries.DeleteExpiredAgentSessions(ctx, now.Add(-agentSessionRetention).Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("clean up old agent sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent session registration: %w", err)
	}
	return nil
}

func (s *Store) AssociateAgentSessionWithProject(ctx context.Context, agentSessionID, projectID string) error {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return err
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return fmt.Errorf("project_id is required")
	}
	projectIDValue := sql.NullString{String: projectID, Valid: true}

	now := time.Now().UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent session association: %w", err)
	}
	defer tx.Rollback()

	queries := sqlcgen.New(s.db).WithTx(tx)
	result, err := queries.UpdateAgentSessionProject(ctx, sqlcgen.UpdateAgentSessionProjectParams{
		ProjectID: projectIDValue,
		ID:        agentSessionID,
	})
	if err != nil {
		return fmt.Errorf("associate agent session with project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect agent session association: %w", err)
	}
	if affected == 0 {
		if err := queries.InsertAgentSessionAssociation(ctx, sqlcgen.InsertAgentSessionAssociationParams{
			ID:           agentSessionID,
			ProjectID:    projectIDValue,
			RegisteredAt: registeredAt,
		}); err != nil {
			return fmt.Errorf("insert associated agent session: %w", err)
		}
	}

	if err := queries.DeleteExpiredAgentSessionsExcept(ctx, sqlcgen.DeleteExpiredAgentSessionsExceptParams{
		ID:           agentSessionID,
		RegisteredAt: now.Add(-agentSessionRetention).Format(time.RFC3339Nano),
	}); err != nil {
		return fmt.Errorf("clean up old agent sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent session association: %w", err)
	}
	return nil
}

func (s *Store) LatestAgentSessionID(ctx context.Context, projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("project_id is required")
	}

	var agentSessionID string
	var err error
	agentSessionID, err = sqlcgen.New(s.db).GetLatestAgentSessionID(ctx, sql.NullString{String: projectID, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find latest agent session: %w", err)
	}
	return agentSessionID, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }
