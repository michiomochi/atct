package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
	_ "modernc.org/sqlite"
)

type inboxStaleResponse struct {
	UnappliedDecisions []struct {
		ID              int64      `json:"id"`
		StaleUnapplied  bool       `json:"stale_unapplied"`
		OwnerLastSeenAt *time.Time `json:"owner_last_seen_at"`
	} `json:"unapplied_decisions"`
	StalledProjects []struct {
		ProjectID   int64      `json:"project_id"`
		ProjectName string     `json:"project_name"`
		LastSeenAt  *time.Time `json:"last_seen_at"`
	} `json:"stalled_projects"`
}

func TestInboxStaleUnappliedAndStalledProjects(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "atct.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	// A second handle on the same file, to backdate timestamps the store
	// would otherwise set to now.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	now := time.Now().UTC()
	newProject := func(name string) int64 {
		p, err := s.CreateProject(ctx, name, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	newGoal := func(projectID int64) int64 {
		g, err := s.CreateGoal(ctx, projectID, "goal", "human")
		if err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	// answered creates an answered decision and backdates its answer.
	answered := func(goalID int64, kind domain.DecisionKind, ago time.Duration) int64 {
		d, err := s.AskDecision(ctx, store.AskInput{GoalID: goalID, Kind: kind, Question: "q"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AnswerDecision(ctx, store.AnswerInput{DecisionID: d.ID, AnswerLabel: "yes"}); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.ExecContext(ctx, `UPDATE decisions SET answered_at = ? WHERE id = ?`,
			now.Add(-ago).Format(time.RFC3339), d.ID); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	upsertMonitor := func(projectID, goalID int64, role string, lastSeen time.Time) {
		goal := goalID
		h := store.MonitorHealth{
			CWD: t.TempDir(), Role: role, State: "healthy", Reason: "test",
			ProjectID: projectID, GoalID: &goal, PID: 4242,
			ProcessStartedAt: lastSeen.Add(-time.Minute), TransitionedAt: lastSeen, LastSeenAt: lastSeen,
		}
		h.MonitorID = store.MonitorHealthID(h.CWD, h.Role, h.ProjectID, h.GoalID, nil, h.PID, h.ProcessStartedAt)
		if err := s.UpsertMonitorHealth(ctx, h); err != nil {
			t.Fatal(err)
		}
	}

	// Goal with a stale answer and a live subcommander.
	live := newProject("live")
	liveGoal := newGoal(live)
	upsertMonitor(live, liveGoal, "subcommander", now.Add(-time.Minute))
	staleID := answered(liveGoal, domain.KindDecision, 11*time.Minute)
	freshID := answered(liveGoal, domain.KindDecision, 9*time.Minute)
	reviewID := answered(liveGoal, domain.KindGoalReview, 11*time.Minute)

	// Silent for 31 minutes with an active goal: stalled. Its goal has no
	// answer, so only the stalled list matters here.
	quiet := newProject("quiet")
	quietGoal := newGoal(quiet)
	upsertMonitor(quiet, quietGoal, "subcommander", now.Add(-31*time.Minute))

	// No monitor ever, active goal: stalled with a null last_seen_at.
	never := newProject("never")
	newGoal(never)

	// Archived project with an active goal: not listed.
	archived := newProject("archived")
	newGoal(archived)
	if _, err := s.ArchiveProject(ctx, archived); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(s).Handler())
	t.Cleanup(server.Close)
	response, err := server.Client().Get(server.URL + "/api/inbox")
	if err != nil {
		t.Fatalf("GET /api/inbox: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/inbox status = %d", response.StatusCode)
	}
	var got inboxStaleResponse
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	byID := make(map[int64]int)
	for i, d := range got.UnappliedDecisions {
		byID[d.ID] = i
	}
	check := func(id int64, wantStale, wantOwner bool) {
		t.Helper()
		i, ok := byID[id]
		if !ok {
			t.Fatalf("decision %d missing from unapplied_decisions", id)
		}
		d := got.UnappliedDecisions[i]
		if d.StaleUnapplied != wantStale {
			t.Errorf("decision %d stale_unapplied = %v, want %v", id, d.StaleUnapplied, wantStale)
		}
		if (d.OwnerLastSeenAt != nil) != wantOwner {
			t.Errorf("decision %d owner_last_seen_at = %v, want present=%v", id, d.OwnerLastSeenAt, wantOwner)
		}
	}
	check(staleID, true, true)
	check(freshID, false, false)
	check(reviewID, false, false)

	stalled := make(map[int64]*time.Time)
	for _, p := range got.StalledProjects {
		stalled[p.ProjectID] = p.LastSeenAt
	}
	if _, ok := stalled[quiet]; !ok {
		t.Error("quiet project (31 minutes silent) not in stalled_projects")
	} else if stalled[quiet] == nil {
		t.Error("quiet project last_seen_at = null, want its last heartbeat")
	}
	if last, ok := stalled[never]; !ok || last != nil {
		t.Errorf("never-monitored project: present=%v last_seen_at=%v, want present with null", ok, last)
	}
	if _, ok := stalled[live]; ok {
		t.Error("live project listed in stalled_projects")
	}
	if _, ok := stalled[archived]; ok {
		t.Error("archived project listed in stalled_projects")
	}
}
