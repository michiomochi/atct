package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func archiveProjectViaHTTP(t *testing.T, f *fixture, srvURL string, client *http.Client, action string) domain.Project {
	t.Helper()
	status, _, body := doRequest(t, client, http.MethodPost, srvURL+"/api/projects/"+idText(f.project.ID)+"/"+action, nil)
	if status != http.StatusOK {
		t.Fatalf("%s status = %d; body=%s", action, status, body)
	}
	var p domain.Project
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHTTPProjectArchiveRoundTripAndIdempotence(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	c := srv.Client()

	first := archiveProjectViaHTTP(t, f, srv.URL, c, "archive")
	if first.ArchivedAt == nil {
		t.Fatal("archive returned no archived_at")
	}
	if again := archiveProjectViaHTTP(t, f, srv.URL, c, "archive"); again.ArchivedAt == nil || !again.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("second archive = %+v, want original archived_at", again)
	}

	status, _, body := doRequest(t, c, http.MethodGet, srv.URL+"/api/projects", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"archived_at"`) {
		t.Fatalf("GET /api/projects = %d %s; want archived_at", status, body)
	}

	if un := archiveProjectViaHTTP(t, f, srv.URL, c, "unarchive"); un.ArchivedAt != nil {
		t.Fatalf("unarchive = %+v", un)
	}
	if un := archiveProjectViaHTTP(t, f, srv.URL, c, "unarchive"); un.ArchivedAt != nil {
		t.Fatalf("second unarchive = %+v", un)
	}
	status, _, body = doRequest(t, c, http.MethodGet, srv.URL+"/api/projects/"+idText(f.project.ID)+"/archive", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("GET archive status = %d; body=%s", status, body)
	}
}

func TestHTTPArchivedProjectRefusesWritesAndAllowsReads(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	c := srv.Client()
	archiveProjectViaHTTP(t, f, srv.URL, c, "archive")

	writes := []struct {
		name, path string
		body       any
	}{
		{"create goal", "/api/goals", map[string]any{"project_id": f.project.ID, "content": "x", "creator": "human"}},
		{"answer decision", "/api/decisions/" + idText(f.open.ID) + "/answer", map[string]string{"answer_text": "yes"}},
		{"withdraw goal", "/api/goals/" + idText(f.goal.ID) + "/withdraw", map[string]string{"reason": "r"}},
		{"snooze task", "/api/tasks/" + idText(f.tasks[0].ID) + "/snooze", map[string]string{"snoozed_until": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}},
		{"release task", "/api/tasks/" + idText(f.tasks[0].ID) + "/release", map[string]string{}},
	}
	check := func(wantRefused bool) {
		t.Helper()
		for _, w := range writes {
			status, _, body := doRequest(t, c, http.MethodPost, srv.URL+w.path, mustJSON(t, w.body))
			if !wantRefused {
				if status == http.StatusConflict {
					t.Errorf("%s: 409 after unarchive; body=%s", w.name, body)
				}
				continue
			}
			if status != http.StatusConflict {
				t.Errorf("%s: status = %d, want 409; body=%s", w.name, status, body)
				continue
			}
			for _, want := range []string{f.project.Name, "atct project unarchive"} {
				if !strings.Contains(string(body), want) {
					t.Errorf("%s: body %s lacks %q", w.name, body, want)
				}
			}
		}
	}
	check(true)

	for _, path := range []string{"/api/goals/" + idText(f.goal.ID), "/api/tasks/" + idText(f.tasks[0].ID)} {
		if status, _, body := doRequest(t, c, http.MethodGet, srv.URL+path, nil); status != http.StatusOK {
			t.Errorf("GET %s = %d; body=%s", path, status, body)
		}
	}

	status, _, body := doRequest(t, c, http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var inbox struct {
		OpenDecisions []struct {
			ID int64 `json:"id"`
		} `json:"open_decisions"`
		ActiveGoals []struct {
			ID int64 `json:"id"`
		} `json:"active_goals"`
	}
	if err := json.Unmarshal(body, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox.OpenDecisions) != 0 || len(inbox.ActiveGoals) != 0 {
		t.Fatalf("inbox shows archived project rows: %s", body)
	}

	archiveProjectViaHTTP(t, f, srv.URL, c, "unarchive")
	check(false)
}

func TestHTTPGoalDetailReportsProjectArchived(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	c := srv.Client()

	archived := func() bool {
		t.Helper()
		status, _, body := doRequest(t, c, http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
		if status != http.StatusOK {
			t.Fatalf("goal status = %d; body=%s", status, body)
		}
		var response struct {
			Goal struct {
				ProjectArchived *bool `json:"project_archived"`
			} `json:"goal"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if response.Goal.ProjectArchived == nil {
			t.Fatalf("goal has no project_archived; body=%s", body)
		}
		return *response.Goal.ProjectArchived
	}
	if archived() {
		t.Fatal("project_archived = true for an active project")
	}
	archiveProjectViaHTTP(t, f, srv.URL, c, "archive")
	if !archived() {
		t.Fatal("project_archived = false for an archived project")
	}
	archiveProjectViaHTTP(t, f, srv.URL, c, "unarchive")
	if archived() {
		t.Fatal("project_archived = true after unarchive")
	}
}
