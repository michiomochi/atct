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
	RecoveryProofLeaseLapsed     RecoveryProofKind   = "heartbeat_lease_lapsed"
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

// CanRecoverSession answers whether a session's work can be taken from it.
//
// It delegates to the predicate the recovery writes themselves use. Having a
// second copy here is what let the lease reach one of them and not the other:
// this one was fixed to read the lease while every real recovery kept asking
// the operating system about a pid that is the daemon's own.
func (s *Store) CanRecoverSession(ctx context.Context, sessionID int64) (RecoveryProof, error) {
	proof, err := canRecoverSessionInTx(ctx, sqlcgen.New(s.db), sessionID)
	if err != nil {
		return RecoveryProof{}, err
	}
	return proof.RecoveryProof, nil
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
	// Holding the project's claim already says which project this is, so
	// comparing it again only adds a way to fail. Having no project at all is
	// different: identify binds one, so a session without one never identified
	// from inside a registered project and has no business discarding another.
	if !commander.ProjectID.Valid {
		return fmt.Errorf("commander session %d has no project: it never identified from inside one: %w", sessionID, ErrSessionDiscardForbidden)
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
	// Two different refusals, so the answer says which one it is. A session in
	// another project is out of this commander's reach; a session with no
	// project never identified from inside one, and revoking it would be a
	// guess about what it belongs to.
	if !target.ProjectID.Valid {
		return domain.Decision{}, fmt.Errorf("target session %d has no project: it never identified from inside one: %w", in.TargetSessionID, ErrSessionDiscardForbidden)
	}
	if target.ProjectID.Int64 != in.ProjectID {
		return domain.Decision{}, fmt.Errorf("target session %d is in project %d, not %d: %w", in.TargetSessionID, target.ProjectID.Int64, in.ProjectID, ErrSessionDiscardForbidden)
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
	now := formatTimestamp(time.Now())
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
