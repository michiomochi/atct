package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

const (
	MonitorHealthLease     = 75 * time.Second
	monitorHealthRetention = 24 * time.Hour
)

type MonitorHealth struct {
	MonitorID string `json:"monitor_id"`
	// MonitorToken is not stored. It names the binding the monitor already
	// holds, which is the only record of the session it serves, and the server
	// resolves it into AgentSessionID on the way in.
	MonitorToken     string     `json:"monitor_token,omitempty"`
	AgentKey         string     `json:"agent_key,omitempty"`
	ScopeKey         string     `json:"scope_key,omitempty"`
	AgentSessionID   int64      `json:"agent_session_id,omitempty"`
	CWD              string     `json:"cwd"`
	Role             string     `json:"role"`
	State            string     `json:"state"`
	Reason           string     `json:"reason,omitempty"`
	ProjectID        int64      `json:"project_id"`
	GoalID           *int64     `json:"goal_id,omitempty"`
	TaskID           *int64     `json:"task_id,omitempty"`
	PID              int        `json:"pid"`
	ProcessStartedAt time.Time  `json:"process_started_at"`
	TransitionedAt   time.Time  `json:"transitioned_at"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
	StoppedAt        *time.Time `json:"stopped_at,omitempty"`
}

func MonitorHealthID(cwd, role string, projectID int64, goalID, taskID *int64, pid int, processStartedAt time.Time, scopeKeys ...string) string {
	absCWD, err := filepath.Abs(filepath.Clean(strings.TrimSpace(cwd)))
	if err != nil {
		return ""
	}
	selector := fmt.Sprintf("%s\x00%d\x00", role, projectID)
	if goalID != nil {
		selector += fmt.Sprintf("goal:%d\x00", *goalID)
	}
	if taskID != nil {
		selector += fmt.Sprintf("task:%d\x00", *taskID)
	}
	if len(scopeKeys) > 0 && strings.TrimSpace(scopeKeys[0]) != "" {
		selector += fmt.Sprintf("scope:%s\x00", strings.TrimSpace(scopeKeys[0]))
	}
	identity := fmt.Sprintf("%s\x00%s\x00%s%d\x00%s", absCWD, selector, "pid:", pid, processStartedAt.UTC().Format(time.RFC3339Nano))
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("monitor-%x", digest[:])
}

// ScopedMonitorHealthID derives the monitor identity for a lifecycle-owned
// orchestration scope. The scope generation is part of the identity so a
// process reusing the same selector cannot refresh an older handoff record.
func ScopedMonitorHealthID(cwd, role string, projectID int64, goalID, taskID *int64, pid int, processStartedAt time.Time, scopeKey string) string {
	return MonitorHealthID(cwd, role, projectID, goalID, taskID, pid, processStartedAt, scopeKey)
}

func validateMonitorHealth(health MonitorHealth) error {
	if strings.TrimSpace(health.MonitorID) == "" || strings.TrimSpace(health.CWD) == "" || strings.TrimSpace(health.Role) == "" {
		return errors.New("monitor identity is required")
	}
	if health.Role != "commander" && health.Role != "subcommander" && health.Role != "executor" {
		return fmt.Errorf("monitor role %q is not eligible", health.Role)
	}
	if health.ProjectID <= 0 || health.PID <= 0 || health.ProcessStartedAt.IsZero() {
		return errors.New("monitor project, pid, and process start are required")
	}
	if health.Role == "commander" && (health.GoalID != nil || health.TaskID != nil) {
		return errors.New("commander monitor cannot carry goal or task selector")
	}
	if health.Role == "subcommander" && health.TaskID != nil {
		return errors.New("subcommander monitor cannot carry task selector")
	}
	if health.Role == "executor" && health.TaskID == nil {
		return errors.New("executor monitor requires task selector")
	}
	if health.Role != "commander" && health.GoalID == nil {
		return errors.New("monitor goal selector is required")
	}
	if expected := MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt, health.ScopeKey); expected != health.MonitorID {
		return errors.New("monitor identity does not match its process and scope")
	}
	return nil
}

func (s *Store) UpsertMonitorHealth(ctx context.Context, health MonitorHealth) error {
	if err := validateMonitorHealth(health); err != nil {
		return err
	}
	// The binding wins over whatever the client reported. A bound monitor is
	// told its token, never the id of the session behind it, so a value it
	// supplies can only be a guess from the environment, and a stale one would
	// otherwise outrank the record that actually joins the two.
	if token := strings.TrimSpace(health.MonitorToken); token != "" {
		// A missing binding is not an error: the monitor can report health
		// before the session it serves has identified itself.
		if agentSessionID, err := sqlcgen.New(s.db).GetMonitorBindingAgentSessionID(ctx, token); err == nil {
			health.AgentSessionID = agentSessionID
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("resolve monitor binding for health: %w", err)
		}
	}
	now := time.Now().UTC()
	if health.LastSeenAt.IsZero() {
		health.LastSeenAt = now
	}
	if health.TransitionedAt.IsZero() {
		health.TransitionedAt = now
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin monitor health upsert: %w", err)
	}
	defer tx.Rollback()
	cutoff := now.Add(-monitorHealthRetention).UTC().Format(time.RFC3339Nano)
	queries := sqlcgen.New(tx)
	if err := queries.PruneMonitorHealth(ctx, cutoff); err != nil {
		return fmt.Errorf("prune monitor health: %w", err)
	}
	if err := queries.UpsertMonitorHealth(ctx, sqlcgen.UpsertMonitorHealthParams{
		MonitorID:        health.MonitorID,
		AgentKey:         health.AgentKey,
		ScopeKey:         health.ScopeKey,
		AgentSessionID:   health.AgentSessionID,
		Cwd:              health.CWD,
		Role:             health.Role,
		ProjectID:        health.ProjectID,
		GoalID:           nullableMonitorID(health.GoalID),
		TaskID:           nullableMonitorID(health.TaskID),
		Pid:              int64(health.PID),
		ProcessStartedAt: health.ProcessStartedAt.UTC().Format(time.RFC3339Nano),
		State:            health.State,
		Reason:           health.Reason,
		TransitionedAt:   health.TransitionedAt.UTC().Format(time.RFC3339Nano),
		LastSeenAt:       health.LastSeenAt.UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return fmt.Errorf("upsert monitor health: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit monitor health upsert: %w", err)
	}
	return nil
}

func nullableMonitorID(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func (s *Store) StopMonitorHealth(ctx context.Context, monitorID string, stoppedAt time.Time) error {
	if strings.TrimSpace(monitorID) == "" || stoppedAt.IsZero() {
		return errors.New("monitor stop identity and time are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin monitor health stop: %w", err)
	}
	defer tx.Rollback()
	when := stoppedAt.UTC().Format(time.RFC3339Nano)
	result, err := sqlcgen.New(tx).StopMonitorHealth(ctx, sqlcgen.StopMonitorHealthParams{
		StoppedAt:      sql.NullString{String: when, Valid: true},
		TransitionedAt: when,
		LastSeenAt:     when,
		MonitorID:      monitorID,
	})
	if err != nil {
		return fmt.Errorf("stop monitor health: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("inspect monitor health stop: %w", err)
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	cutoff := time.Now().UTC().Add(-monitorHealthRetention).Format(time.RFC3339Nano)
	if err := sqlcgen.New(tx).PruneMonitorHealth(ctx, cutoff); err != nil {
		return fmt.Errorf("prune stopped monitor health: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit monitor health stop: %w", err)
	}
	return nil
}

func (s *Store) ListMonitorHealth(ctx context.Context, projectID int64) ([]MonitorHealth, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	cutoff := time.Now().UTC().Add(-MonitorHealthLease).Format(time.RFC3339Nano)
	rows, err := sqlcgen.New(s.db).ListMonitorHealth(ctx, sqlcgen.ListMonitorHealthParams{ProjectID: projectID, LastSeenAt: cutoff})
	if err != nil {
		return nil, fmt.Errorf("list monitor health: %w", err)
	}
	result := make([]MonitorHealth, 0)
	for _, row := range rows {
		health, err := monitorHealthFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, health)
	}
	return result, nil
}

// ListMonitorHealthHistory returns retained rows, including stopped and stale
// monitors, for recovery wakeup. It is not a liveness view.
func (s *Store) ListMonitorHealthHistory(ctx context.Context, projectID int64) ([]MonitorHealth, error) {
	if projectID <= 0 {
		return nil, errors.New("project_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListMonitorHealthHistory(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list monitor health history: %w", err)
	}
	result := make([]MonitorHealth, 0, len(rows))
	for _, row := range rows {
		health, err := monitorHealthFromFields(row.MonitorID, row.AgentKey, row.ScopeKey, row.AgentSessionID, row.Cwd, row.Role, row.ProjectID, row.GoalID, row.TaskID, row.Pid, row.ProcessStartedAt, row.State, row.Reason, row.TransitionedAt, row.LastSeenAt, row.StoppedAt)
		if err != nil {
			return nil, err
		}
		result = append(result, health)
	}
	return result, nil
}

func monitorHealthFromRow(row sqlcgen.ListMonitorHealthRow) (MonitorHealth, error) {
	return monitorHealthFromFields(row.MonitorID, row.AgentKey, row.ScopeKey, row.AgentSessionID, row.Cwd, row.Role, row.ProjectID, row.GoalID, row.TaskID, row.Pid, row.ProcessStartedAt, row.State, row.Reason, row.TransitionedAt, row.LastSeenAt, row.StoppedAt)
}

func monitorHealthFromFields(monitorID, agentKey, scopeKey string, agentSessionID int64, cwd, role string, projectID int64, goalID, taskID sql.NullInt64, pid int64, processStartedAt, state, reason, transitionedAt, lastSeenAt string, stoppedAt sql.NullString) (MonitorHealth, error) {
	health := MonitorHealth{
		MonitorID:      monitorID,
		AgentKey:       agentKey,
		ScopeKey:       scopeKey,
		AgentSessionID: agentSessionID,
		CWD:            cwd,
		Role:           role,
		ProjectID:      projectID,
		PID:            int(pid),
		State:          state,
		Reason:         reason,
	}
	if goalID.Valid {
		value := goalID.Int64
		health.GoalID = &value
	}
	if taskID.Valid {
		value := taskID.Int64
		health.TaskID = &value
	}
	var err error
	if health.ProcessStartedAt, err = time.Parse(time.RFC3339Nano, processStartedAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse monitor process start: %w", err)
	}
	if health.TransitionedAt, err = time.Parse(time.RFC3339Nano, transitionedAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse monitor transition: %w", err)
	}
	if health.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt); err != nil {
		return MonitorHealth{}, fmt.Errorf("parse monitor last seen: %w", err)
	}
	if stoppedAt.Valid {
		value, err := time.Parse(time.RFC3339Nano, stoppedAt.String)
		if err != nil {
			return MonitorHealth{}, fmt.Errorf("parse monitor stop: %w", err)
		}
		health.StoppedAt = &value
	}
	return health, nil
}

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
	transportSession, err := queries.GetAgentSessionRecovery(ctx, agentSessionID)
	if err != nil {
		return 0, false, fmt.Errorf("find agent session discard state for identification: %w", err)
	}
	if agentSessionHasDiscardMetadata(transportSession) {
		return 0, false, fmt.Errorf("agent session %d is discarded: %w", agentSessionID, ErrSessionDiscarded)
	}

	canonicalID = agentSessionID
	existingID, lookupErr := queries.GetAgentSessionIDByKey(ctx, sessionKey)
	if lookupErr == nil {
		canonicalID = existingID
		reattached = existingID != agentSessionID
		canonicalSession, err := queries.GetAgentSessionRecovery(ctx, canonicalID)
		if err != nil {
			return 0, false, fmt.Errorf("find canonical agent session discard state: %w", err)
		}
		if agentSessionHasDiscardMetadata(canonicalSession) {
			return 0, false, fmt.Errorf("canonical agent session %d is discarded: %w", canonicalID, ErrSessionDiscarded)
		}
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

// AgentSessionIDByKey resolves the stable harness session identity recorded by
// session.identify to ATCT's canonical agent session.
func (s *Store) AgentSessionIDByKey(ctx context.Context, sessionKey string) (int64, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0, fmt.Errorf("session_key is required")
	}
	id, err := sqlcgen.New(s.db).GetAgentSessionIDByKey(ctx, sessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		// "sql: no rows in result set" names the query, not the cause. The
		// cause is always the same: nothing ever registered this key.
		return 0, fmt.Errorf("session_key %q is not registered: this session never ran atct_session_identify (or the ATCT database was reset since it did). Call atct_session_identify with the session_key and monitor_token from SessionStart, then retry", sessionKey)
	}
	return id, err
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
