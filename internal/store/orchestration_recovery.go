package store

import (
	"context"
	"fmt"
	"strings"
)

const (
	OrchestrationRecoveryRecipientMismatch = "recipient_mismatch"
	OrchestrationRecoveryMonitorMissing    = "monitor_missing"
	OrchestrationRecoveryCommander         = "commander"
)

// OrchestrationRecovery is a derived, commander-routable instruction for an
// active monitor scope that cannot currently be satisfied by its rightful
// live wrapper. It deliberately contains no delivery receipt or ownership
// state; the existing watch delivery map owns in-process deduplication.
type OrchestrationRecovery struct {
	Condition              string `json:"condition"`
	TargetRole             string `json:"target_role"`
	ProjectID              int64  `json:"project_id"`
	GoalID                 *int64 `json:"goal_id,omitempty"`
	TaskID                 *int64 `json:"task_id,omitempty"`
	HandoffID              string `json:"handoff_id,omitempty"`
	ScopeKey               string `json:"scope_key"`
	Generation             string `json:"generation"`
	ExpectedRole           string `json:"expected_role"`
	ExpectedAgentSessionID int64  `json:"expected_agent_session_id,omitempty"`
	ExpectedAgentKey       string `json:"expected_agent_key,omitempty"`
	ObservedRole           string `json:"observed_role,omitempty"`
	ObservedAgentSessionID int64  `json:"observed_agent_session_id,omitempty"`
	ObservedAgentKey       string `json:"observed_agent_key,omitempty"`
	Instruction            string `json:"instruction"`
}

// ListOrchestrationRecoveries compares active lifecycle-owned scopes with the
// live health rows already used by ListMatchingMonitorHealth. Stale and
// stopped rows are absent from that live view and therefore resolve to the
// same safe monitor_missing route as an absent wrapper.
func (s *Store) ListOrchestrationRecoveries(ctx context.Context, scopes []OrchestrationScope) ([]OrchestrationRecovery, error) {
	healthByProject, err := s.listMonitorHealthForScopes(ctx, scopes)
	if err != nil {
		return nil, err
	}
	return orchestrationRecoveriesForScopes(scopes, healthByProject), nil
}

func (s *Store) listMonitorHealthForScopes(ctx context.Context, scopes []OrchestrationScope) (map[int64][]MonitorHealth, error) {
	healthByProject := make(map[int64][]MonitorHealth)
	loadedProjects := make(map[int64]bool)
	for _, scope := range scopes {
		if !scope.Active || scope.ProjectID <= 0 {
			continue
		}
		if !loadedProjects[scope.ProjectID] {
			health, err := s.ListMonitorHealth(ctx, scope.ProjectID)
			if err != nil {
				return nil, err
			}
			healthByProject[scope.ProjectID] = health
			loadedProjects[scope.ProjectID] = true
		}
	}
	return healthByProject, nil
}

func matchingMonitorHealthForScopes(scopes []OrchestrationScope, healthByProject map[int64][]MonitorHealth) []MonitorHealth {
	matched := make([]MonitorHealth, 0)
	seen := make(map[string]bool)
	for _, scope := range scopes {
		if !scope.Active || scope.ProjectID <= 0 {
			continue
		}
		for _, health := range healthByProject[scope.ProjectID] {
			if !MonitorHealthMatchesScope(health, scope) || seen[health.MonitorID] {
				continue
			}
			seen[health.MonitorID] = true
			matched = append(matched, health)
		}
	}
	return matched
}

func orchestrationRecoveriesForScopes(scopes []OrchestrationScope, healthByProject map[int64][]MonitorHealth) []OrchestrationRecovery {
	recoveries := make([]OrchestrationRecovery, 0)
	for _, scope := range scopes {
		if !orchestrationRecoveryScope(scope) {
			continue
		}

		var observed *MonitorHealth
		matched := false
		for i := range healthByProject[scope.ProjectID] {
			health := healthByProject[scope.ProjectID][i]
			if MonitorHealthMatchesScope(health, scope) {
				matched = true
				break
			}
			if observed == nil && health.ScopeKey == scope.ScopeKey {
				candidate := health
				observed = &candidate
			}
		}
		if matched {
			continue
		}

		condition := OrchestrationRecoveryMonitorMissing
		if observed != nil {
			condition = OrchestrationRecoveryRecipientMismatch
		}
		recoveries = append(recoveries, newOrchestrationRecovery(scope, condition, observed))
	}
	return recoveries
}

// Commander recovery is meaningful for child wrappers whose missing or
// mismatched health needs a project-level instruction. The commander scope is
// the destination for that instruction, not a source of child recovery.
func orchestrationRecoveryScope(scope OrchestrationScope) bool {
	if !scope.Active || scope.ProjectID <= 0 || strings.TrimSpace(scope.ScopeKey) == "" {
		return false
	}
	return scope.Role == "subcommander" || scope.Role == "executor"
}

func newOrchestrationRecovery(scope OrchestrationScope, condition string, observed *MonitorHealth) OrchestrationRecovery {
	recovery := OrchestrationRecovery{
		Condition:              condition,
		TargetRole:             OrchestrationRecoveryCommander,
		ProjectID:              scope.ProjectID,
		GoalID:                 cloneOrchestrationID(scope.GoalID),
		TaskID:                 cloneOrchestrationID(scope.TaskID),
		HandoffID:              orchestrationScopeHandoffID(scope.ScopeKey),
		ScopeKey:               scope.ScopeKey,
		Generation:             scope.SourceGeneration,
		ExpectedRole:           scope.Role,
		ExpectedAgentSessionID: scope.AgentSessionID,
		ExpectedAgentKey:       scope.AgentKey,
		Instruction:            orchestrationRecoveryInstruction(scope, condition, observed),
	}
	if observed != nil {
		recovery.ObservedRole = observed.Role
		recovery.ObservedAgentSessionID = observed.AgentSessionID
		recovery.ObservedAgentKey = observed.AgentKey
	}
	return recovery
}

func cloneOrchestrationID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func orchestrationScopeHandoffID(scopeKey string) string {
	parts := strings.SplitN(strings.TrimSpace(scopeKey), ":", 4)
	if len(parts) == 4 && (parts[0] == "goal" || parts[0] == "task") {
		return parts[3]
	}
	return ""
}

func orchestrationRecoveryInstruction(scope OrchestrationScope, condition string, observed *MonitorHealth) string {
	if condition == OrchestrationRecoveryRecipientMismatch {
		return fmt.Sprintf(
			"reissue or transfer %s to a freshly role-validated %s receiver (expected role %s, session %d, agent key %s; observed role %s, session %d, agent key %s), then have the receiver run session identification, receive, and role validation before work",
			orchestrationRecoverySubject(scope), scope.Role, scope.Role, scope.AgentSessionID,
			recoveryIdentityValue(scope.AgentKey), observed.Role, observed.AgentSessionID,
			recoveryIdentityValue(observed.AgentKey),
		)
	}
	return fmt.Sprintf(
		"start the rightful monitored wrapper for scope %s with %s",
		scope.ScopeKey, orchestrationMonitorCommand(scope),
	)
}

func orchestrationRecoverySubject(scope OrchestrationScope) string {
	if handoffID := orchestrationScopeHandoffID(scope.ScopeKey); handoffID != "" {
		return fmt.Sprintf("handoff %s (scope %s)", handoffID, scope.ScopeKey)
	}
	return "scope " + scope.ScopeKey
}

func recoveryIdentityValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<none>"
	}
	return value
}

func orchestrationMonitorCommand(scope OrchestrationScope) string {
	switch scope.Role {
	case "subcommander":
		if scope.GoalID != nil {
			return fmt.Sprintf("atct codex monitor --role subcommander --goal %d --", *scope.GoalID)
		}
	case "executor":
		if scope.TaskID != nil {
			return fmt.Sprintf("atct codex monitor --role executor --task %d --", *scope.TaskID)
		}
	}
	return "atct codex monitor --role " + strings.TrimSpace(scope.Role) + " --"
}
