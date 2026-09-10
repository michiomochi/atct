package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	RecoveryProofProcessMismatch RecoveryProofKind   = "process_identity_mismatch"
	RecoveryProofSessionDiscard  RecoveryProofKind   = "session_discard"
	SessionDiscardDecisionKind   domain.DecisionKind = "session_discard"
)

type RecoveryProofKind string

// RecoveryProof is the durable evidence that permits replacing a session's
// ownership. Unknown and live sessions never produce a proof.
type RecoveryProof struct {
	SessionID         int64
	Kind              RecoveryProofKind
	DiscardDecisionID int64
}

type HandoffRecovery struct {
	ID                int64
	HandoffKind       string
	HandoffID         string
	GoalID            *int64
	TaskID            *int64
	RecoveredPhase    string
	StaleSessionID    int64
	ProofKind         RecoveryProofKind
	DiscardDecisionID *int64
	RecoveredBy       int64
	ReplacementID     string
	Reason            string
	CreatedAt         time.Time
}

type HandoffRecoveryInput struct {
	HandoffKind    string
	HandoffID      string
	GoalID         *int64
	TaskID         *int64
	RecoveredPhase string
	StaleSessionID int64
	Proof          RecoveryProof
	RecoveredBy    int64
	ReplacementID  string
	Reason         string
}

type SessionDiscardRequest struct {
	ProjectID       int64
	GoalID          int64
	TargetSessionID int64
	RequestedBy     int64
	Reason          string
}

func agentSessionHasDiscardMetadata(row sqlcgen.GetAgentSessionRecoveryRow) bool {
	return row.DiscardedAt.Valid || row.DiscardedBy.Valid || row.DiscardedDecisionID.Valid || strings.TrimSpace(row.DiscardReason) != ""
}

// requireUndiscardedSession fences every handoff transition that identifies a
// caller. Legacy callers may use an unregistered zero/session ID, so only a
// durable discard record is rejected here.
func (s *Store) requireUndiscardedSession(ctx context.Context, sessionID int64) error {
	if sessionID == 0 {
		return nil
	}
	row, err := sqlcgen.New(s.db).GetAgentSessionRecovery(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find agent session %d discard state: %w", sessionID, err)
	}
	if agentSessionHasDiscardMetadata(row) {
		return fmt.Errorf("agent session %d is discarded: %w", sessionID, ErrSessionDiscarded)
	}
	return nil
}

var (
	ErrSessionRecoveryNotProven = errors.New("agent session cannot be proven stale")
	ErrSessionDiscarded         = errors.New("agent session has been discarded")
	ErrSessionDiscardNotFound   = errors.New("session discard decision not found")
	ErrSessionDiscardPending    = errors.New("session discard decision is not approved")
	ErrSessionDiscardForbidden  = errors.New("session discard requires the project commander")
)

// CanRecoverSession returns proof only for a process identity that is
// definitely stale or for a session explicitly revoked by a human decision.
func (s *Store) CanRecoverSession(ctx context.Context, sessionID int64) (RecoveryProof, error) {
	if sessionID <= 0 {
		return RecoveryProof{}, fmt.Errorf("session id is required: %w", ErrSessionRecoveryNotProven)
	}
	row, err := sqlcgen.New(s.db).GetAgentSessionRecovery(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryProof{}, fmt.Errorf("agent session %d is not registered: %w", sessionID, ErrAgentSessionNotRegistered)
	}
	if err != nil {
		return RecoveryProof{}, fmt.Errorf("find agent session %d for recovery: %w", sessionID, err)
	}
	hasDiscardMetadata := agentSessionHasDiscardMetadata(row)
	if hasDiscardMetadata {
		if !row.DiscardedAt.Valid || strings.TrimSpace(row.DiscardedAt.String) == "" ||
			!row.DiscardedBy.Valid || row.DiscardedBy.Int64 <= 0 ||
			!row.DiscardedDecisionID.Valid || row.DiscardedDecisionID.Int64 <= 0 ||
			strings.TrimSpace(row.DiscardReason) == "" {
			return RecoveryProof{}, fmt.Errorf("agent session %d has an incomplete discard record: %w", sessionID, ErrSessionRecoveryNotProven)
		}
		proof := RecoveryProof{SessionID: sessionID, Kind: RecoveryProofSessionDiscard}
		proof.DiscardDecisionID = row.DiscardedDecisionID.Int64
		return proof, nil
	}
	if !claimIsDefinitelyDead(ctx, s, sessionID) {
		return RecoveryProof{}, fmt.Errorf("agent session %d is live or its liveness is unknown: %w", sessionID, ErrSessionRecoveryNotProven)
	}
	return RecoveryProof{SessionID: sessionID, Kind: RecoveryProofProcessMismatch}, nil
}

type sessionDiscardDecisionPayload struct {
	ProjectID       int64  `json:"project_id"`
	TargetSessionID int64  `json:"target_session_id"`
	Reason          string `json:"reason"`
}

func (s *Store) requireProjectCommander(ctx context.Context, q *sqlcgen.Queries, projectID, sessionID int64) error {
	if projectID <= 0 || sessionID <= 0 {
		return fmt.Errorf("project and commander session ids are required: %w", ErrSessionDiscardForbidden)
	}
	project, err := q.GetProject(ctx, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("project %d not found: %w", projectID, ErrSessionDiscardForbidden)
	}
	if err != nil {
		return fmt.Errorf("find project %d for session discard: %w", projectID, err)
	}
	if project.ClaimedBy != sessionID || !claimIsRunningWithQueries(ctx, q, sessionID) {
		return fmt.Errorf("session %d is not the live commander for project %d: %w", sessionID, projectID, ErrSessionDiscardForbidden)
	}
	commander, err := q.GetAgentSessionRecovery(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("commander session %d is not registered: %w", sessionID, ErrSessionDiscardForbidden)
	}
	if err != nil {
		return fmt.Errorf("find commander session %d: %w", sessionID, err)
	}
	if agentSessionHasDiscardMetadata(commander) {
		return fmt.Errorf("commander session %d is discarded: %w", sessionID, ErrSessionDiscardForbidden)
	}
	if !commander.ProjectID.Valid || commander.ProjectID.Int64 != projectID {
		return fmt.Errorf("commander session %d is outside project %d: %w", sessionID, projectID, ErrSessionDiscardForbidden)
	}
	return nil
}

func (s *Store) sessionDiscardPayload(ctx context.Context, q *sqlcgen.Queries, decisionID, projectID, targetSessionID, commanderID int64) (sessionDiscardDecisionPayload, error) {
	decision, err := q.GetDecision(ctx, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decision %d not found: %w", decisionID, ErrSessionDiscardNotFound)
	}
	if err != nil {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("find session discard decision %d: %w", decisionID, err)
	}
	if decision.Kind != string(SessionDiscardDecisionKind) || decision.AgentSessionID != commanderID || decision.Status != string(domain.DecisionApplied) || decision.AnswerLabel != "approve" {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decision %d is not an approved session discard: %w", decisionID, ErrSessionDiscardPending)
	}
	goalProjectID, err := q.GetGoalProjectID(ctx, decision.GoalID)
	if err != nil {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("find project for session discard decision %d: %w", decisionID, err)
	}
	if goalProjectID != projectID {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decision %d belongs to project %d, not %d: %w", decisionID, goalProjectID, projectID, ErrSessionDiscardForbidden)
	}
	start := strings.Index(decision.Question, "{")
	if start < 0 {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decision %d has malformed session discard payload: %w", decisionID, ErrSessionDiscardPending)
	}
	var payload sessionDiscardDecisionPayload
	if err := json.Unmarshal([]byte(decision.Question[start:]), &payload); err != nil {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decode session discard decision %d: %w", decisionID, ErrSessionDiscardPending)
	}
	if payload.ProjectID != projectID || payload.TargetSessionID != targetSessionID || strings.TrimSpace(payload.Reason) == "" {
		return sessionDiscardDecisionPayload{}, fmt.Errorf("decision %d does not authorize session %d in project %d: %w", decisionID, targetSessionID, projectID, ErrSessionDiscardForbidden)
	}
	return payload, nil
}

// RequestSessionDiscard asks the human to approve revoking a session. The
// request is scoped to an existing goal because the decisions table is
// goal-scoped; the payload also binds the project and target session.
func (s *Store) RequestSessionDiscard(ctx context.Context, in SessionDiscardRequest) (domain.Decision, error) {
	if in.ProjectID <= 0 || in.GoalID <= 0 || in.TargetSessionID <= 0 || in.RequestedBy <= 0 || strings.TrimSpace(in.Reason) == "" {
		return domain.Decision{}, fmt.Errorf("project, goal, target, requester, and reason are required: %w", ErrSessionDiscardForbidden)
	}
	if err := s.requireProjectCommander(ctx, sqlcgen.New(s.db), in.ProjectID, in.RequestedBy); err != nil {
		return domain.Decision{}, err
	}
	goalProjectID, err := sqlcgen.New(s.db).GetGoalProjectID(ctx, in.GoalID)
	if err != nil {
		return domain.Decision{}, fmt.Errorf("find goal %d for session discard: %w", in.GoalID, err)
	}
	if goalProjectID != in.ProjectID {
		return domain.Decision{}, fmt.Errorf("goal %d belongs to project %d, not %d: %w", in.GoalID, goalProjectID, in.ProjectID, ErrSessionDiscardForbidden)
	}
	target, err := sqlcgen.New(s.db).GetAgentSessionRecovery(ctx, in.TargetSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Decision{}, fmt.Errorf("target session %d is not registered: %w", in.TargetSessionID, ErrAgentSessionNotRegistered)
	}
	if err != nil {
		return domain.Decision{}, fmt.Errorf("find target session %d: %w", in.TargetSessionID, err)
	}
	if !target.ProjectID.Valid || target.ProjectID.Int64 != in.ProjectID {
		return domain.Decision{}, fmt.Errorf("target session %d is outside project %d: %w", in.TargetSessionID, in.ProjectID, ErrSessionDiscardForbidden)
	}
	if agentSessionHasDiscardMetadata(target) {
		return domain.Decision{}, fmt.Errorf("target session %d is already discarded: %w", in.TargetSessionID, ErrSessionDiscarded)
	}
	payload, err := json.Marshal(sessionDiscardDecisionPayload{ProjectID: in.ProjectID, TargetSessionID: in.TargetSessionID, Reason: in.Reason})
	if err != nil {
		return domain.Decision{}, fmt.Errorf("encode session discard request: %w", err)
	}
	return s.AskDecision(ctx, AskInput{
		GoalID:   in.GoalID,
		Kind:     SessionDiscardDecisionKind,
		Question: "Approve this session discard? " + string(payload),
		Options: []domain.Option{
			{Label: "approve", Description: "Discard the target agent session", Consequence: "The session can no longer mutate handoffs"},
			{Label: "reject", Description: "Keep the target agent session", Consequence: "No session state changes"},
		},
		AgentSessionID: in.RequestedBy,
	})
}

// DiscardSession commits an approved human revocation. Authorization and the
// target's not-yet-discarded state are checked in the same transaction.
func (s *Store) DiscardSession(ctx context.Context, projectID, targetSessionID, decisionID, commanderID int64) error {
	if projectID <= 0 || targetSessionID <= 0 || decisionID <= 0 || commanderID <= 0 {
		return fmt.Errorf("project, target, decision, and commander session ids are required: %w", ErrSessionDiscardForbidden)
	}
	if err := s.requireProjectCommander(ctx, sqlcgen.New(s.db), projectID, commanderID); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session discard: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	if err := s.requireProjectCommander(ctx, q, projectID, commanderID); err != nil {
		return err
	}
	payload, err := s.sessionDiscardPayload(ctx, q, decisionID, projectID, targetSessionID, commanderID)
	if err != nil {
		return err
	}
	target, err := q.GetAgentSessionRecovery(ctx, targetSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("target session %d is not registered: %w", targetSessionID, ErrAgentSessionNotRegistered)
	}
	if err != nil {
		return fmt.Errorf("find target session %d: %w", targetSessionID, err)
	}
	if !target.ProjectID.Valid || target.ProjectID.Int64 != projectID {
		return fmt.Errorf("target session %d is outside project %d: %w", targetSessionID, projectID, ErrSessionDiscardForbidden)
	}
	if agentSessionHasDiscardMetadata(target) {
		return fmt.Errorf("target session %d is already discarded: %w", targetSessionID, ErrSessionDiscarded)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := q.DiscardAgentSession(ctx, sqlcgen.DiscardAgentSessionParams{
		DiscardedAt:         sql.NullString{String: now, Valid: true},
		DiscardedBy:         sql.NullInt64{Int64: commanderID, Valid: true},
		DiscardedDecisionID: sql.NullInt64{Int64: decisionID, Valid: true},
		DiscardReason:       payload.Reason,
		ID:                  targetSessionID,
		ProjectID:           sql.NullInt64{Int64: projectID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("discard agent session %d: %w", targetSessionID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect session discard %d: %w", targetSessionID, err)
	}
	if affected != 1 {
		return fmt.Errorf("target session %d is already discarded: %w", targetSessionID, ErrSessionDiscarded)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session discard %d: %w", targetSessionID, err)
	}
	return nil
}

func validateHandoffRecoveryInput(in HandoffRecoveryInput) error {
	if strings.TrimSpace(in.HandoffKind) == "" || strings.TrimSpace(in.HandoffID) == "" {
		return errors.New("handoff kind and id are required")
	}
	if strings.TrimSpace(in.RecoveredPhase) == "" {
		return errors.New("recovered phase is required")
	}
	if in.StaleSessionID <= 0 || in.RecoveredBy <= 0 {
		return errors.New("stale and recovering session ids are required")
	}
	if in.Proof.SessionID != in.StaleSessionID || (in.Proof.Kind != RecoveryProofProcessMismatch && in.Proof.Kind != RecoveryProofSessionDiscard) {
		return errors.New("recovery proof does not match stale session")
	}
	if in.Proof.Kind == RecoveryProofSessionDiscard && in.Proof.DiscardDecisionID <= 0 {
		return errors.New("session discard proof requires a decision id")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return errors.New("recovery reason is required")
	}
	if in.GoalID == nil && in.TaskID == nil {
		return errors.New("goal_id or task_id is required")
	}
	return nil
}

func nullableRecoveryID(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullableRecoveryText(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func handoffRecoveryFromRow(row sqlcgen.HandoffRecovery) (HandoffRecovery, error) {
	recovery := HandoffRecovery{
		ID:             row.ID,
		HandoffKind:    row.HandoffKind,
		HandoffID:      row.HandoffID,
		RecoveredPhase: row.RecoveredPhase,
		StaleSessionID: row.StaleSessionID,
		ProofKind:      RecoveryProofKind(row.ProofKind),
		RecoveredBy:    row.RecoveredBy,
		ReplacementID:  row.ReplacementID.String,
		Reason:         row.Reason,
	}
	if row.GoalID.Valid {
		value := row.GoalID.Int64
		recovery.GoalID = &value
	}
	if row.TaskID.Valid {
		value := row.TaskID.Int64
		recovery.TaskID = &value
	}
	if row.DiscardDecisionID.Valid {
		value := row.DiscardDecisionID.Int64
		recovery.DiscardDecisionID = &value
	}
	createdAt, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return HandoffRecovery{}, fmt.Errorf("parse handoff recovery created_at: %w", err)
	}
	recovery.CreatedAt = createdAt
	return recovery, nil
}

func handoffRecoveryParams(in HandoffRecoveryInput, createdAt string) sqlcgen.CreateHandoffRecoveryParams {
	return sqlcgen.CreateHandoffRecoveryParams{
		HandoffKind:       in.HandoffKind,
		HandoffID:         in.HandoffID,
		GoalID:            nullableRecoveryID(in.GoalID),
		TaskID:            nullableRecoveryID(in.TaskID),
		RecoveredPhase:    in.RecoveredPhase,
		StaleSessionID:    in.StaleSessionID,
		ProofKind:         string(in.Proof.Kind),
		DiscardDecisionID: sql.NullInt64{Int64: in.Proof.DiscardDecisionID, Valid: in.Proof.DiscardDecisionID != 0},
		RecoveredBy:       in.RecoveredBy,
		ReplacementID:     nullableRecoveryText(in.ReplacementID),
		Reason:            in.Reason,
		CreatedAt:         createdAt,
	}
}

func getHandoffRecovery(ctx context.Context, q *sqlcgen.Queries, in HandoffRecoveryInput) (HandoffRecovery, error) {
	row, err := q.GetHandoffRecovery(ctx, sqlcgen.GetHandoffRecoveryParams{
		HandoffKind:    in.HandoffKind,
		HandoffID:      in.HandoffID,
		RecoveredPhase: in.RecoveredPhase,
		StaleSessionID: in.StaleSessionID,
	})
	if err != nil {
		return HandoffRecovery{}, err
	}
	return handoffRecoveryFromRow(row)
}

// RecordHandoffRecovery appends one audit event. The unique key makes exact
// retries return the original event without rewriting its reason or evidence.
func (s *Store) RecordHandoffRecovery(ctx context.Context, in HandoffRecoveryInput) (HandoffRecovery, error) {
	if err := validateHandoffRecoveryInput(in); err != nil {
		return HandoffRecovery{}, err
	}
	if existing, err := getHandoffRecovery(ctx, sqlcgen.New(s.db), in); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return HandoffRecovery{}, fmt.Errorf("find existing handoff recovery: %w", err)
	}
	proof, err := s.CanRecoverSession(ctx, in.StaleSessionID)
	if err != nil {
		return HandoffRecovery{}, err
	}
	if proof.Kind != in.Proof.Kind || proof.DiscardDecisionID != in.Proof.DiscardDecisionID {
		return HandoffRecovery{}, fmt.Errorf("recovery proof changed for session %d: %w", in.StaleSessionID, ErrSessionRecoveryNotProven)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HandoffRecovery{}, fmt.Errorf("begin handoff recovery audit: %w", err)
	}
	defer tx.Rollback()
	createdAt := time.Now().UTC().Format(time.RFC3339Nano)
	q := sqlcgen.New(tx)
	if _, err := q.CreateHandoffRecovery(ctx, handoffRecoveryParams(in, createdAt)); err != nil {
		return HandoffRecovery{}, fmt.Errorf("append handoff recovery audit: %w", err)
	}
	recovery, err := getHandoffRecovery(ctx, q, in)
	if err != nil {
		return HandoffRecovery{}, fmt.Errorf("read handoff recovery audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return HandoffRecovery{}, fmt.Errorf("commit handoff recovery audit: %w", err)
	}
	return recovery, nil
}

func (s *Store) ListHandoffRecoveriesForGoal(ctx context.Context, goalID int64) ([]HandoffRecovery, error) {
	if goalID <= 0 {
		return nil, errors.New("goal_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListHandoffRecoveriesForGoal(ctx, sql.NullInt64{Int64: goalID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list goal handoff recoveries: %w", err)
	}
	return convertHandoffRecoveries(rows)
}

func (s *Store) ListHandoffRecoveriesForTask(ctx context.Context, taskID int64) ([]HandoffRecovery, error) {
	if taskID <= 0 {
		return nil, errors.New("task_id is required")
	}
	rows, err := sqlcgen.New(s.db).ListHandoffRecoveriesForTask(ctx, sql.NullInt64{Int64: taskID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list task handoff recoveries: %w", err)
	}
	return convertHandoffRecoveries(rows)
}

func convertHandoffRecoveries(rows []sqlcgen.HandoffRecovery) ([]HandoffRecovery, error) {
	out := make([]HandoffRecovery, 0, len(rows))
	for _, row := range rows {
		recovery, err := handoffRecoveryFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, recovery)
	}
	return out, nil
}
