package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/michiomochi/atct/internal/store"
)

// archiveExemptMethods may run against an archived project: reads, identity and
// recovery, and the archive controls themselves. Every other method is
// refused for an archived project; archive_guard_test.go keeps the two sets
// covering every method the handler dispatches.
var archiveExemptMethods = map[string]bool{
	"run.register":               true,
	"monitor.capability.issue":   true,
	"session.capability.issue":   true,
	"session.identify":           true,
	"session.discard.request":    true,
	"session.discard":            true,
	"session.role":               true,
	"session.stop_check":         true,
	"session.monitor_check":      true,
	"development.start":          true,
	"handoff.recover":            true,
	"handoff.entry.history":      true,
	"goal.handoff.entry.history": true,
	"project.create":             true,
	"project.list":               true,
	"project.archive":            true,
	"project.unarchive":          true,
	"project.release":            true,
	"goal.list":                  true,
	"goal.get":                   true,
	"goal.sessions":              true,
	"review.exchange.list":       true,
}

// ensureProjectActiveForMethod refuses a method that would change an archived
// project. The project is found from the first of goal_id, task_id,
// decision_id, project_id, cwd that params carries; params that name no
// project, or one that does not exist, pass through to the method's own errors.
func (d *Daemon) ensureProjectActiveForMethod(ctx context.Context, method string, params json.RawMessage) error {
	if archiveExemptMethods[method] {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil || fields == nil {
		return nil
	}
	projectID, err := d.archiveGuardProjectID(ctx, fields)
	if err != nil || projectID == 0 {
		return nil
	}
	if err := d.store.EnsureProjectActive(ctx, projectID); err != nil && errors.Is(err, store.ErrProjectArchived) {
		return err
	}
	return nil
}

func (d *Daemon) archiveGuardProjectID(ctx context.Context, fields map[string]json.RawMessage) (int64, error) {
	id := func(key string) int64 {
		var v int64
		if raw, ok := fields[key]; ok && json.Unmarshal(raw, &v) == nil {
			return v
		}
		return 0
	}
	if v := id("goal_id"); v != 0 {
		goal, err := d.store.GetGoal(ctx, v)
		return goal.ProjectID, err
	}
	if v := id("task_id"); v != 0 {
		return d.store.ProjectIDForTask(ctx, v)
	}
	if v := id("decision_id"); v != 0 {
		decision, err := d.store.GetDecision(ctx, v)
		if err != nil {
			return 0, err
		}
		goal, err := d.store.GetGoal(ctx, decision.GoalID)
		return goal.ProjectID, err
	}
	if v := id("project_id"); v != 0 {
		return v, nil
	}
	var cwd string
	if raw, ok := fields["cwd"]; ok && json.Unmarshal(raw, &cwd) == nil && strings.TrimSpace(cwd) != "" {
		project, err := d.store.ResolveProject(ctx, cwd)
		return project.ID, err
	}
	return 0, nil
}
