package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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

func requireAgentSessionID(agentSessionID int64) (int64, error) {
	if agentSessionID <= 0 {
		return 0, fmt.Errorf("agent_session_id is required")
	}
	return agentSessionID, nil
}

func (s *Store) RegisterAgentSession(ctx context.Context, pid int) (int64, error) {
	return s.RegisterAgentSessionInProject(ctx, pid, 0)
}

func (s *Store) RegisterAgentSessionInProject(ctx context.Context, pid int, projectID int64) (int64, error) {
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
		return 0, fmt.Errorf("begin agent session registration: %w", err)
	}
	defer tx.Rollback()

	queries := sqlcgen.New(s.db).WithTx(tx)
	projectIDValue := sql.NullInt64{Int64: projectID, Valid: projectID != 0}
	agentSessionID, err := queries.RegisterAgentSessionWithProject(ctx, sqlcgen.RegisterAgentSessionWithProjectParams{
		ProjectID:    projectIDValue,
		Pid:          int64(storedPID),
		StartedAt:    startedAt,
		RegisteredAt: registeredAt,
	})
	if err != nil {
		return 0, fmt.Errorf("register agent session: %w", err)
	}
	if err := queries.DeleteExpiredAgentSessions(ctx, now.Add(-agentSessionRetention).Format(time.RFC3339Nano)); err != nil {
		return 0, fmt.Errorf("clean up old agent sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit agent session registration: %w", err)
	}
	return agentSessionID, nil
}

func (s *Store) IdentifyAgentSession(ctx context.Context, agentSessionID int64, sessionKey string) (canonicalID int64, reattached bool, err error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0, false, fmt.Errorf("session_key is required")
	}
	agentSessionID, err = requireAgentSessionID(agentSessionID)
	if err != nil {
		return 0, false, err
	}

	storedPID := 0
	startedAt := ""
	if actualStartedAt, processErr := processStartedAt(os.Getpid()); processErr == nil {
		storedPID = os.Getpid()
		startedAt = actualStartedAt
	}
	now := time.Now().UTC()
	registeredAt := now.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, fmt.Errorf("begin agent session identification: %w", err)
	}
	defer tx.Rollback()

	queries := sqlcgen.New(s.db).WithTx(tx)
	if _, err := queries.GetAgentSessionLiveness(ctx, agentSessionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("agent session %d is not registered: %w", agentSessionID, ErrAgentSessionNotRegistered)
		}
		return 0, false, fmt.Errorf("find agent session for identification: %w", err)
	}

	canonicalID = agentSessionID
	existingID, lookupErr := queries.GetAgentSessionIDByKey(ctx, sessionKey)
	if lookupErr == nil {
		canonicalID = existingID
		reattached = existingID != agentSessionID
		if err := queries.UpdateAgentSessionProcessIdentity(ctx, sqlcgen.UpdateAgentSessionProcessIdentityParams{
			Pid:          int64(storedPID),
			StartedAt:    startedAt,
			RegisteredAt: registeredAt,
			ID:           canonicalID,
		}); err != nil {
			return 0, false, fmt.Errorf("update canonical agent session identity: %w", err)
		}
	} else if errors.Is(lookupErr, sql.ErrNoRows) {
		if err := queries.UpdateAgentSessionKey(ctx, sqlcgen.UpdateAgentSessionKeyParams{
			SessionKey: sessionKey,
			ID:         agentSessionID,
		}); err != nil {
			return 0, false, fmt.Errorf("set agent session key: %w", err)
		}
		if err := queries.UpdateAgentSessionProcessIdentity(ctx, sqlcgen.UpdateAgentSessionProcessIdentityParams{
			Pid:          int64(storedPID),
			StartedAt:    startedAt,
			RegisteredAt: registeredAt,
			ID:           agentSessionID,
		}); err != nil {
			return 0, false, fmt.Errorf("update agent session identity: %w", err)
		}
	} else {
		return 0, false, fmt.Errorf("find agent session by key: %w", lookupErr)
	}

	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("commit agent session identification: %w", err)
	}
	return canonicalID, reattached, nil
}

func (s *Store) AssociateAgentSessionWithProject(ctx context.Context, agentSessionID int64, projectID int64) error {
	agentSessionID, err := requireAgentSessionID(agentSessionID)
	if err != nil {
		return err
	}
	projectIDNullable := sql.NullInt64{Int64: projectID, Valid: true}

	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent session association: %w", err)
	}
	defer tx.Rollback()

	queries := sqlcgen.New(s.db).WithTx(tx)
	result, err := queries.UpdateAgentSessionProject(ctx, sqlcgen.UpdateAgentSessionProjectParams{
		ProjectID: projectIDNullable,
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
		return fmt.Errorf("agent session %d is not registered: %w", agentSessionID, ErrAgentSessionNotRegistered)
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

func (s *Store) LatestAgentSessionID(ctx context.Context, projectID int64) (int64, error) {
	id := projectID

	var (
		agentSessionID int64
		err            error
	)
	agentSessionID, err = sqlcgen.New(s.db).GetLatestAgentSessionID(ctx, sql.NullInt64{Int64: id, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find latest agent session: %w", err)
	}
	return agentSessionID, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }
