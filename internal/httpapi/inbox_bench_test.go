package httpapi_test

import (
	"context"
	"database/sql"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

// Load harness for GET /api/inbox. The fixture is synthetic and sized like the
// live daemon: 400 goals (19 active), 1700 tasks, 20000 agent_sessions. It makes
// no timing assertions; read the ns/op it prints.
//
//	go test -run xxx -bench Inbox -benchtime 3x ./internal/httpapi/

const (
	inbenchGoals         = 400
	inbenchActiveGoals   = 19
	inbenchTasks         = 1700
	inbenchAgentSessions = 20000
	inbenchPollers       = 33
	inbenchPollInterval  = 5 * time.Second
	inbenchWriteInterval = 5 * time.Second
)

type inboxBenchFixture struct {
	store   *store.Store
	client  *http.Client
	url     string
	session []int64
}

func newInboxBenchFixture(b *testing.B) *inboxBenchFixture {
	b.Helper()
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "atct.db")
	db, err := store.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	project, err := db.CreateProject(ctx, "inbox-bench", b.TempDir())
	if err != nil {
		b.Fatal(err)
	}

	// Bulk rows go through a second connection; the store's single connection
	// is left to the code under test. Goal ids are 1..inbenchGoals on a fresh file.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		b.Fatal(err)
	}
	defer raw.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		// Done goals carry their completion report (goals CHECK constraint on status 'done').
		{`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < ?)
		  INSERT INTO goals (project_id, content, spec, plan, status, creator, created_at, updated_at,
		                     work_done, now_possible, how_to_verify, surprises, needs_review)
		  SELECT ?, 'Bench goal ' || n || char(10) || char(10) || 'synthetic body', '', '',
		         CASE WHEN n <= ? THEN 'active' WHEN n % 2 = 0 THEN 'done' ELSE 'dropped' END,
		         'human', ?, ?,
		         CASE WHEN n > ? AND n % 2 = 0 THEN 'synthetic' ELSE '' END,
		         CASE WHEN n > ? AND n % 2 = 0 THEN 'synthetic' ELSE '' END,
		         CASE WHEN n > ? AND n % 2 = 0 THEN 'synthetic' ELSE '' END,
		         CASE WHEN n > ? AND n % 2 = 0 THEN 'synthetic' ELSE '' END,
		         CASE WHEN n > ? AND n % 2 = 0 THEN 'synthetic' ELSE '' END
		  FROM seq`,
			[]any{inbenchGoals, project.ID, inbenchActiveGoals, now, now,
				inbenchActiveGoals, inbenchActiveGoals, inbenchActiveGoals, inbenchActiveGoals, inbenchActiveGoals}},
		{`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < ?)
		  INSERT INTO tasks (goal_id, title, description, status, agent, sort_order, declare_key, created_at, updated_at)
		  SELECT ((n-1) % ?) + 1, 'Bench task ' || n, '', CASE WHEN n % 10 = 0 THEN 'todo' ELSE 'done' END,
		         'subcommander', n, 'bench-' || n, ?, ? FROM seq`,
			[]any{inbenchTasks, inbenchGoals, now, now}},
		{`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n < ?)
		  INSERT INTO agent_sessions (project_id, registered_at, pid, started_at, session_key, development_mode)
		  SELECT ?, ?, n, ?, '', 0 FROM seq`,
			[]any{inbenchAgentSessions, project.ID, now, now}},
	} {
		if _, err := raw.Exec(stmt.query, stmt.args...); err != nil {
			b.Fatal(err)
		}
	}

	f := &inboxBenchFixture{store: db}
	rows, err := raw.Query(`SELECT id FROM agent_sessions ORDER BY id`)
	if err != nil {
		b.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		f.session = append(f.session, id)
	}
	rows.Close()

	srv := httptest.NewServer(httpapi.New(db).Handler())
	b.Cleanup(srv.Close)
	f.client = srv.Client()
	f.url = srv.URL + "/api/inbox"
	return f
}

func getInbox(b *testing.B, client *http.Client, url string) {
	b.Helper()
	resp, err := client.Get(url)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		b.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b.Fatalf("GET /api/inbox status = %d", resp.StatusCode)
	}
}

// BenchmarkInbox measures one GET /api/inbox with nothing else running.
func BenchmarkInbox(b *testing.B) {
	f := newInboxBenchFixture(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getInbox(b, f.client, f.url)
	}
}

// BenchmarkInboxUnderLoad measures GET /api/inbox while inboxPollers pollers
// poll every 5s and writers renew agent sessions every 5s, the shape of the
// live daemon's watches and heartbeats. Each op is one probe request.
func BenchmarkInboxUnderLoad(b *testing.B) {
	f := newInboxBenchFixture(b)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	b.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	for i := 0; i < inbenchPollers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Random phase: pollers started together stay in lockstep and arrive as one burst.
			sleepCtx(ctx, time.Duration(rand.Int63n(int64(inbenchPollInterval))))
			for ctx.Err() == nil {
				resp, err := f.client.Get(f.url)
				if err == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				sleepCtx(ctx, inbenchPollInterval)
			}
		}()
	}
	for i := 0; i < inbenchPollers; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			sleepCtx(ctx, time.Duration(rand.Int63n(int64(inbenchWriteInterval))))
			for n := offset; ctx.Err() == nil; n += inbenchPollers {
				_ = f.store.HeartbeatAgentSession(ctx, f.session[n%len(f.session)], time.Now().UTC())
				sleepCtx(ctx, inbenchWriteInterval)
			}
		}(i)
	}
	// Let the background load reach steady state before the probe starts.
	sleepCtx(ctx, inbenchWriteInterval)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		getInbox(b, f.client, f.url)
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
