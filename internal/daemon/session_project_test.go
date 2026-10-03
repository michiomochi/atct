package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/michiomochi/atct/internal/rpc"
)

func sessionProjectID(t *testing.T, fixture goalListFixture, agentSessionID int64) (int64, bool) {
	t.Helper()
	var projectID *int64
	if err := fixture.store.DB().QueryRowContext(context.Background(),
		`SELECT project_id FROM agent_sessions WHERE id = ?`, agentSessionID).Scan(&projectID); err != nil {
		t.Fatalf("read session project: %v", err)
	}
	if projectID == nil {
		return 0, false
	}
	return *projectID, true
}

func identify(t *testing.T, fixture goalListFixture, agentSessionID int64, sessionKey, cwd string) {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"agent_session_id": agentSessionID,
		"session_key":      sessionKey,
		"cwd":              cwd,
	})
	if err != nil {
		t.Fatalf("marshal session.identify params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "session.identify", Params: params}); err != nil {
		t.Fatalf("session.identify: %v", err)
	}
}

// The project used to arrive on whichever later call happened to pass through
// ensureAgentSessionProject, so a session that never got that far carried no
// project at all. Identify is the one call every session makes first.
func TestSessionIdentifyBindsTheProjectFromCwd(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := daemonTestSessionID(t, fixture.store, "identify-cwd-session")
	if _, bound := sessionProjectID(t, fixture, sessionID); bound {
		t.Fatal("a freshly registered session already has a project")
	}

	identify(t, fixture, sessionID, "identify-cwd", fixture.project.RootPath)

	projectID, bound := sessionProjectID(t, fixture, sessionID)
	if !bound || projectID != fixture.project.ID {
		t.Fatalf("session project = (%d, bound=%v), want project %d", projectID, bound, fixture.project.ID)
	}
}

// A cwd outside any registered project is not an error. The session simply is
// not in one, and refusing to identify would strand it.
func TestSessionIdentifyAcceptsACwdOutsideAnyProject(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := daemonTestSessionID(t, fixture.store, "identify-cwd-outside")
	identify(t, fixture, sessionID, "identify-outside", t.TempDir())

	if _, bound := sessionProjectID(t, fixture, sessionID); bound {
		t.Fatal("a cwd outside every project still bound one")
	}
}

func TestSessionIdentifyWithoutCwdStillWorks(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := daemonTestSessionID(t, fixture.store, "identify-no-cwd")
	identify(t, fixture, sessionID, "identify-no-cwd", "")

	if _, bound := sessionProjectID(t, fixture, sessionID); bound {
		t.Fatal("a session that named no cwd bound a project anyway")
	}
}
