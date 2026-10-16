package store

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

// reclaimFixture makes sessions of a known age, measured against f.now, the clock
// the test passes to the reclaim.
type reclaimFixture struct {
	t       *testing.T
	s       *Store
	now     time.Time
	project int64
}

func newReclaimFixture(t *testing.T) *reclaimFixture {
	t.Helper()
	s := newTestStore(t)
	project, err := s.CreateProject(context.Background(), "reclaim", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return &reclaimFixture{t: t, s: s, now: time.Now().UTC(), project: project.ID}
}

// session registers a session whose last heartbeat is age old. identified sets a
// session_key, which an identified session keeps for good.
func (f *reclaimFixture) session(age time.Duration, identified bool) int64 {
	f.t.Helper()
	ctx := context.Background()
	id, err := f.s.RegisterAgentSessionInProject(ctx, 0, f.project)
	if err != nil {
		f.t.Fatalf("RegisterAgentSessionInProject: %v", err)
	}
	key := ""
	if identified {
		key = "reclaim-key-" + strconv.FormatInt(id, 10)
	}
	at := formatTimestamp(f.now.Add(-age))
	if _, err := f.s.DB().ExecContext(ctx, `UPDATE agent_sessions SET registered_at = ?, last_heartbeat_at = ?, session_key = ? WHERE id = ?`, at, at, key, id); err != nil {
		f.t.Fatalf("age session %d: %v", id, err)
	}
	return id
}

func (f *reclaimFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.s.DB().ExecContext(context.Background(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *reclaimFixture) sessionState(id int64) (exists, discarded bool) {
	f.t.Helper()
	var discardedAt any
	err := f.s.DB().QueryRowContext(context.Background(), `SELECT discarded_at FROM agent_sessions WHERE id = ?`, id).Scan(&discardedAt)
	if err != nil {
		return false, false
	}
	return true, discardedAt != nil
}

func (f *reclaimFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.s.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *reclaimFixture) reclaim() ReclaimResult {
	f.t.Helper()
	result, err := f.s.ReclaimAgentSessions(context.Background(), f.now)
	if err != nil {
		f.t.Fatalf("ReclaimAgentSessions: %v", err)
	}
	return result
}

func TestReclaimAgentSessionsDeletesOnlyUnreferencedIdleSessions(t *testing.T) {
	f := newReclaimFixture(t)
	ctx := context.Background()

	unreferenced := f.session(48*time.Hour, false)
	discardedUnreferenced := f.session(48*time.Hour, true)
	f.exec(`UPDATE agent_sessions SET discarded_at = ?, discard_reason = 'test' WHERE id = ?`, formatTimestamp(f.now), discardedUnreferenced)
	claimant := f.session(48*time.Hour, false)
	f.exec(`UPDATE projects SET claimed_by = ?, claimed_at = ? WHERE id = ?`, claimant, formatTimestamp(f.now), f.project)
	decided := f.session(48*time.Hour, false)
	goal, err := f.s.CreateGoal(ctx, f.project, "reclaim goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := f.s.AskDecision(ctx, AskInput{GoalID: goal.ID, Kind: domain.DecisionKind("question"), Question: "Which?", AgentSessionID: decided}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	identified := f.session(48*time.Hour, true)
	recent := f.session(time.Hour, false)
	monitored := f.session(48*time.Hour, false)
	f.exec(`INSERT INTO monitor_health (monitor_id, cwd, role, project_id, pid, process_started_at, state, transitioned_at, last_seen_at, agent_session_id)
		VALUES ('live-monitor', '/tmp', 'executor', ?, 0, ?, 'healthy', ?, ?, ?)`, f.project, formatTimestamp(f.now), formatTimestamp(f.now), formatTimestamp(f.now), monitored)

	result := f.reclaim()
	if result.SessionsDeleted != 2 || result.SessionsDiscarded != 1 {
		t.Fatalf("reclaim = %+v, want 2 deleted (unreferenced, one already discarded) and 1 discarded (decision-referenced)", result)
	}
	if exists, _ := f.sessionState(unreferenced); exists {
		t.Fatalf("unreferenced idle session %d was kept", unreferenced)
	}
	if exists, _ := f.sessionState(discardedUnreferenced); exists {
		t.Fatalf("discarded unreferenced session %d was kept", discardedUnreferenced)
	}
	if exists, discarded := f.sessionState(claimant); !exists || discarded {
		t.Fatalf("project claimant: exists=%v discarded=%v, want kept and untouched", exists, discarded)
	}
	if exists, discarded := f.sessionState(decided); !exists || !discarded {
		t.Fatalf("decision-referenced session: exists=%v discarded=%v, want kept and discarded", exists, discarded)
	}
	if exists, discarded := f.sessionState(identified); !exists || discarded {
		t.Fatalf("identified idle session: exists=%v discarded=%v, want kept and untouched", exists, discarded)
	}
	if exists, discarded := f.sessionState(recent); !exists || discarded {
		t.Fatalf("recent session: exists=%v discarded=%v, want kept and untouched", exists, discarded)
	}
	if exists, discarded := f.sessionState(monitored); !exists || discarded {
		t.Fatalf("session with a live monitor: exists=%v discarded=%v, want kept and untouched", exists, discarded)
	}
}

func TestReclaimAgentSessionsKeepsHandoffHolders(t *testing.T) {
	f := &reclaimFixture{t: t, s: newTestStore(t), now: time.Now().UTC()}
	goalID, holderID := newTaskRecoveryGoal(t, f.s, "reclaim-holder")
	tasks, err := f.s.CreateTasks(context.Background(), goalID, "worker", "reclaim-holder", []string{"implement"}, []string{"implement"})
	if err != nil {
		t.Fatalf("CreateTasks: %v", err)
	}
	if _, err := f.s.RequestTaskHandoff(context.Background(), "reclaim-holder-task", tasks[0].ID, holderID, "work"); err != nil {
		t.Fatalf("RequestTaskHandoff: %v", err)
	}
	// Age the holder and clear its key, so only the handoff keeps it from reclaim.
	f.exec(`UPDATE agent_sessions SET registered_at = ?, last_heartbeat_at = ?, session_key = '' WHERE id = ?`, formatTimestamp(f.now.Add(-48*time.Hour)), formatTimestamp(f.now.Add(-48*time.Hour)), holderID)

	result := f.reclaim()
	if exists, discarded := f.sessionState(holderID); !exists || discarded {
		t.Fatalf("handoff holder: exists=%v discarded=%v (result %+v), want kept and untouched", exists, discarded, result)
	}
}

func TestReclaimAgentSessionsCleansMonitorRows(t *testing.T) {
	f := newReclaimFixture(t)
	live := f.session(48*time.Hour, true)
	stoppedOld := f.session(48*time.Hour, true)
	insertMonitor := func(id string, sessionID int64, stopped *time.Time, seen time.Time) {
		var stoppedAt any
		if stopped != nil {
			stoppedAt = formatTimestamp(*stopped)
		}
		f.exec(`INSERT INTO monitor_health (monitor_id, cwd, role, project_id, pid, process_started_at, state, transitioned_at, last_seen_at, stopped_at, agent_session_id)
			VALUES (?, '/tmp', 'executor', ?, 0, ?, 'healthy', ?, ?, ?, ?)`, id, f.project, formatTimestamp(f.now), formatTimestamp(f.now), formatTimestamp(seen), stoppedAt, sessionID)
	}
	eightDays := f.now.Add(-8 * 24 * time.Hour)
	oneDay := f.now.Add(-24 * time.Hour)
	insertMonitor("stopped-eight-days", stoppedOld, &eightDays, eightDays)
	insertMonitor("stopped-one-day", stoppedOld, &oneDay, oneDay)
	insertMonitor("live", live, nil, f.now)
	insertMonitor("orphan-stale", 999999, nil, eightDays)
	insertMonitor("orphan-live", 999998, nil, f.now)

	f.reclaim()
	for id, want := range map[string]bool{
		"stopped-eight-days": false,
		"stopped-one-day":    true,
		"live":               true,
		"orphan-stale":       false,
		"orphan-live":        true,
	} {
		got := f.count(`SELECT COUNT(*) FROM monitor_health WHERE monitor_id = ?`, id) == 1
		if got != want {
			t.Fatalf("monitor %s present=%v, want %v", id, got, want)
		}
	}
}

func TestReclaimAgentSessionsCleansBindingsOfDiscardedSessions(t *testing.T) {
	f := newReclaimFixture(t)
	discarded := f.session(48*time.Hour, true)
	f.exec(`UPDATE agent_sessions SET discarded_at = ?, discard_reason = 'test' WHERE id = ?`, formatTimestamp(f.now), discarded)
	live := f.session(time.Hour, true)
	stale := f.now.Add(-8 * 24 * time.Hour)
	f.exec(`INSERT INTO monitor_bindings (token, agent_session_id, created_at, last_reconciled_at) VALUES ('stale-token', ?, ?, ?)`, discarded, formatTimestamp(stale), formatTimestamp(stale))
	f.exec(`INSERT INTO monitor_bindings (token, agent_session_id, created_at, last_reconciled_at) VALUES ('live-token', ?, ?, ?)`, live, formatTimestamp(f.now), formatTimestamp(f.now))

	result := f.reclaim()
	if result.MonitorBindings != 1 {
		t.Fatalf("MonitorBindings = %d, want 1", result.MonitorBindings)
	}
	if n := f.count(`SELECT COUNT(*) FROM monitor_bindings WHERE token = 'stale-token'`); n != 0 {
		t.Fatalf("stale binding of a discarded session kept")
	}
	if n := f.count(`SELECT COUNT(*) FROM monitor_bindings WHERE token = 'live-token'`); n != 1 {
		t.Fatalf("binding of a live session removed")
	}
}

func TestReclaimAgentSessionsIsIdempotent(t *testing.T) {
	f := newReclaimFixture(t)
	f.session(48*time.Hour, false)
	decided := f.session(48*time.Hour, false)
	goal, err := f.s.CreateGoal(context.Background(), f.project, "idempotent goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if _, err := f.s.AskDecision(context.Background(), AskInput{GoalID: goal.ID, Kind: domain.DecisionKind("question"), Question: "Which?", AgentSessionID: decided}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	first := f.reclaim()
	if first.Total() == 0 {
		t.Fatalf("first reclaim touched nothing")
	}
	second := f.reclaim()
	if second.Total() != 0 {
		t.Fatalf("second reclaim = %+v, want nothing", second)
	}
}

func TestReclaimAgentSessionsHonorsRowBudget(t *testing.T) {
	f := newReclaimFixture(t)
	for i := 0; i < 5; i++ {
		f.session(48*time.Hour, false)
	}
	ctx := context.Background()
	for pass, want := range []int64{2, 2, 1} {
		result, err := f.s.reclaimAgentSessionsWithBudget(ctx, f.now, 2)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if result.SessionsDeleted != want {
			t.Fatalf("pass %d deleted %d, want %d", pass, result.SessionsDeleted, want)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM agent_sessions WHERE session_key = ''`); n != 0 {
		t.Fatalf("%d unidentified sessions left after the budgeted passes", n)
	}
}

// TestReclaimCatalogueCoversEverySessionColumn fails when a table gains a column
// that names an agent session and the reclaim does not know it. Foreign keys are
// read from the schema; columns that hold a session id without a foreign key are
// found by name (*_by, *_session_id, claimed_by).
func TestReclaimCatalogueCoversEverySessionColumn(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	known := make(map[sessionRef]bool)
	for _, ref := range append(append([]sessionRef{}, protectedSessionRefs...), heldSessionRefs...) {
		known[ref] = true
	}

	var missing []string
	rows, err := s.DB().QueryContext(ctx, `
		SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
		WHERE m.type = 'table'
		  AND (p.name LIKE '%\_by' ESCAPE '\' OR p.name LIKE '%session_id' OR p.name = 'claimed_by')`)
	if err != nil {
		t.Fatalf("list session-like columns: %v", err)
	}
	seen := make(map[sessionRef]bool)
	for rows.Next() {
		var ref sessionRef
		if err := rows.Scan(&ref.table, &ref.column); err != nil {
			t.Fatal(err)
		}
		seen[ref] = true
		if !known[ref] {
			missing = append(missing, fmt.Sprintf("%s.%s", ref.table, ref.column))
		}
	}
	rows.Close()

	fkRows, err := s.DB().QueryContext(ctx, `
		SELECT m.name, p."from" FROM sqlite_master m, pragma_foreign_key_list(m.name) p
		WHERE m.type = 'table' AND p."table" = 'agent_sessions'`)
	if err != nil {
		t.Fatalf("list foreign keys: %v", err)
	}
	for fkRows.Next() {
		var ref sessionRef
		if err := fkRows.Scan(&ref.table, &ref.column); err != nil {
			t.Fatal(err)
		}
		seen[ref] = true
		if !known[ref] {
			missing = append(missing, fmt.Sprintf("%s.%s (foreign key)", ref.table, ref.column))
		}
	}
	fkRows.Close()

	for ref := range known {
		if !seen[ref] {
			t.Errorf("catalogue names %s.%s, which no session column matches; remove it or fix the name", ref.table, ref.column)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("reclaim does not know these session columns; add them to protectedSessionRefs or heldSessionRefs: %v", missing)
	}

	// The reclaim statements name the same columns as the catalogue, in both directions.
	sqlText, err := os.ReadFile("queries/agent_session_reclaim.sql")
	if err != nil {
		t.Fatalf("read reclaim queries: %v", err)
	}
	inSQL := make(map[sessionRef]bool)
	for _, match := range reclaimRefPattern.FindAllStringSubmatch(string(sqlText), -1) {
		if match[1] != match[3] {
			continue // SELECT id FROM t WHERE other IS NOT NULL is a filter, not a reference
		}
		inSQL[sessionRef{table: match[2], column: match[1]}] = true
	}
	for ref := range known {
		if !inSQL[ref] {
			t.Errorf("queries/agent_session_reclaim.sql does not name %s.%s; the reclaim would not protect it", ref.table, ref.column)
		}
	}
	for ref := range inSQL {
		if !known[ref] {
			t.Errorf("queries/agent_session_reclaim.sql names %s.%s, which the catalogue does not list", ref.table, ref.column)
		}
	}
}

// reclaimRefPattern matches one reference in the reclaim queries: SELECT col FROM table WHERE col IS NOT NULL.
var reclaimRefPattern = regexp.MustCompile(`SELECT (\w+) FROM (\w+) WHERE (\w+) IS NOT NULL`)
