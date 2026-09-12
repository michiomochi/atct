package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

func TestMonitorBindingEndpointReturnsPendingBeforeIdentify(t *testing.T) {
	f := newBareFixture(t)
	got := getMonitorBinding(t, f, "pending-token")
	if !got.Pending {
		t.Fatalf("binding = %+v, want pending", got)
	}
}

func TestMonitorBindingEndpointTracksAssignmentTransitions(t *testing.T) {
	f := newBareFixture(t)
	session, err := f.store.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	commander, err := f.store.RegisterAgentSession(f.ctx, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	const token = "monitor-token"
	if err := f.store.BindMonitorToken(f.ctx, token, session); err != nil {
		t.Fatal(err)
	}
	assertMonitorAssignment(t, f, token, store.MonitorAssignment{Role: "executor"})

	if _, err := f.store.ClaimProject(f.ctx, f.project.ID, session); err != nil {
		t.Fatal(err)
	}
	assertMonitorAssignment(t, f, token, store.MonitorAssignment{Role: "commander", ProjectID: f.project.ID})

	if err := f.store.ReleaseProject(f.ctx, f.project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimProject(f.ctx, f.project.ID, commander); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.RequestGoalHandoff(f.ctx, "monitor-goal-handoff", f.goal.ID, commander, "delegate"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ReceiveGoalHandoff(f.ctx, "monitor-goal-handoff", f.goal.ID, session); err != nil {
		t.Fatal(err)
	}
	assertMonitorAssignment(t, f, token, store.MonitorAssignment{Role: "subcommander", ProjectID: f.project.ID, GoalID: f.goal.ID})

	if _, err := f.store.CompleteGoalHandoff(f.ctx, "monitor-goal-handoff", f.goal.ID, "done"); err != nil {
		t.Fatal(err)
	}
	assertMonitorAssignment(t, f, token, store.MonitorAssignment{Role: "executor"})
}

func assertMonitorAssignment(t *testing.T, f *fixture, token string, want store.MonitorAssignment) {
	t.Helper()
	got := getMonitorBinding(t, f, token)
	if got.Pending || !reflect.DeepEqual(got.Assignment, want) {
		t.Fatalf("binding = %+v, want assignment %+v", got, want)
	}
}

func getMonitorBinding(t *testing.T, f *fixture, token string) store.MonitorBinding {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/monitor-bindings/"+token, nil)
	response := httptest.NewRecorder()
	httpapi.New(f.store).ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.Bytes())
	}
	var binding store.MonitorBinding
	if err := json.Unmarshal(response.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	return binding
}
