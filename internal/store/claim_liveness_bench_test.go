package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestClaimLivenessMeasurements(t *testing.T) {
	path := os.Getenv("ATCT_REAL_DB")
	if path == "" {
		t.Skip("set ATCT_REAL_DB to a copy of a real database")
	}

	ctx := context.Background()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	counter := &queryCounter{}
	if err := installCountingDB(t, s, path, counter); err != nil {
		t.Fatalf("install counting database: %v", err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var projectID int64
	var projectRoot string
	for _, project := range projects {
		root := strings.TrimRight(project.RootPath, string(os.PathSeparator))
		if strings.HasSuffix(root, string(os.PathSeparator)+"michiomochi"+string(os.PathSeparator)+"atct") {
			projectID = project.ID
			projectRoot = project.RootPath
			break
		}
	}
	if projectID == 0 {
		t.Fatalf("project root ending in /michiomochi/atct not found")
	}

	goals, tasks, openTaskHandoffs, openGoalHandoffs := measurementRowCounts(t, ctx, s.db, projectID)
	t.Logf("dataset project_id=%d root_path=%q goals=%d tasks=%d open_task_handoffs=%d open_goal_handoffs=%d", projectID, projectRoot, goals, tasks, openTaskHandoffs, openGoalHandoffs)

	counter.reset()
	if _, err := s.ListGoals(ctx, projectID); err != nil {
		t.Fatalf("ListGoals probe: %v", err)
	}
	if got := counter.load(); got != 1 {
		t.Fatalf("ListGoals query count = %d; want 1", got)
	}
	t.Logf("query-count probe ListGoals=1")

	legacyClaimQueries := measureQueries(t, counter, func() error {
		_, _, err := claimLivenessLegacy(ctx, s, projectID)
		return err
	})
	claimQueries := measureQueries(t, counter, func() error {
		_, _, err := ClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("query-count ClaimLiveness legacy=%d new=%d", legacyClaimQueries, claimQueries)
	wantLegacyClaimQueries := int64(1) + goals + tasks + openTaskHandoffs
	wantClaimQueries := int64(1) + openTaskHandoffs
	if legacyClaimQueries != wantLegacyClaimQueries {
		t.Fatalf("legacy ClaimLiveness query count = %d; want %d", legacyClaimQueries, wantLegacyClaimQueries)
	}
	if claimQueries != wantClaimQueries {
		t.Fatalf("ClaimLiveness query count = %d; want %d", claimQueries, wantClaimQueries)
	}

	legacyGoalClaimQueries := measureQueries(t, counter, func() error {
		_, _, err := goalClaimLivenessLegacy(ctx, s, projectID)
		return err
	})
	goalClaimQueries := measureQueries(t, counter, func() error {
		_, _, err := GoalClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("query-count GoalClaimLiveness legacy=%d new=%d", legacyGoalClaimQueries, goalClaimQueries)
	wantLegacyGoalClaimQueries := int64(1) + goals + openGoalHandoffs
	wantGoalClaimQueries := int64(1) + openGoalHandoffs
	if legacyGoalClaimQueries != wantLegacyGoalClaimQueries {
		t.Fatalf("legacy GoalClaimLiveness query count = %d; want %d", legacyGoalClaimQueries, wantLegacyGoalClaimQueries)
	}
	if goalClaimQueries != wantGoalClaimQueries {
		t.Fatalf("GoalClaimLiveness query count = %d; want %d", goalClaimQueries, wantGoalClaimQueries)
	}

	detectWakeupQueries := measureQueries(t, counter, func() error {
		_, err := s.DetectWakeup(ctx, projectID)
		return err
	})
	t.Logf("query-count DetectWakeup=%d", detectWakeupQueries)

	const singleSamples = 10
	legacyClaimSingle := measureDurations(t, singleSamples, func() error {
		_, _, err := claimLivenessLegacy(ctx, s, projectID)
		return err
	})
	claimSingle := measureDurations(t, singleSamples, func() error {
		_, _, err := ClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("duration ClaimLiveness legacy_samples=%d median_ms=%.3f new_samples=%d median_ms=%.3f", singleSamples, durationMillis(medianDuration(legacyClaimSingle)), singleSamples, durationMillis(medianDuration(claimSingle)))

	legacyGoalClaimSingle := measureDurations(t, singleSamples, func() error {
		_, _, err := goalClaimLivenessLegacy(ctx, s, projectID)
		return err
	})
	goalClaimSingle := measureDurations(t, singleSamples, func() error {
		_, _, err := GoalClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("duration GoalClaimLiveness legacy_samples=%d median_ms=%.3f new_samples=%d median_ms=%.3f", singleSamples, durationMillis(medianDuration(legacyGoalClaimSingle)), singleSamples, durationMillis(medianDuration(goalClaimSingle)))

	const parallelSamples = 20
	legacyClaimParallel := measureParallelDurations(t, parallelSamples, func() error {
		_, _, err := claimLivenessLegacy(ctx, s, projectID)
		return err
	})
	claimParallel := measureParallelDurations(t, parallelSamples, func() error {
		_, _, err := ClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("duration ClaimLiveness legacy_samples=%d min_ms=%.3f median_ms=%.3f max_ms=%.3f new_samples=%d min_ms=%.3f median_ms=%.3f max_ms=%.3f", parallelSamples, durationMillis(legacyClaimParallel[0]), durationMillis(medianDuration(legacyClaimParallel)), durationMillis(legacyClaimParallel[len(legacyClaimParallel)-1]), parallelSamples, durationMillis(claimParallel[0]), durationMillis(medianDuration(claimParallel)), durationMillis(claimParallel[len(claimParallel)-1]))

	legacyGoalClaimParallel := measureParallelDurations(t, parallelSamples, func() error {
		_, _, err := goalClaimLivenessLegacy(ctx, s, projectID)
		return err
	})
	goalClaimParallel := measureParallelDurations(t, parallelSamples, func() error {
		_, _, err := GoalClaimLiveness(ctx, s, projectID)
		return err
	})
	t.Logf("duration GoalClaimLiveness legacy_samples=%d min_ms=%.3f median_ms=%.3f max_ms=%.3f new_samples=%d min_ms=%.3f median_ms=%.3f max_ms=%.3f", parallelSamples, durationMillis(legacyGoalClaimParallel[0]), durationMillis(medianDuration(legacyGoalClaimParallel)), durationMillis(legacyGoalClaimParallel[len(legacyGoalClaimParallel)-1]), parallelSamples, durationMillis(goalClaimParallel[0]), durationMillis(medianDuration(goalClaimParallel)), durationMillis(goalClaimParallel[len(goalClaimParallel)-1]))
	if claimParallel[len(claimParallel)-1] >= legacyClaimParallel[len(legacyClaimParallel)-1] {
		t.Fatalf("ClaimLiveness parallel maximum = %s; want less than legacy %s", claimParallel[len(claimParallel)-1], legacyClaimParallel[len(legacyClaimParallel)-1])
	}
}

func measurementRowCounts(t *testing.T, ctx context.Context, db *sql.DB, projectID int64) (goals, tasks, openTaskHandoffs, openGoalHandoffs int64) {
	t.Helper()
	queries := []struct {
		name  string
		query string
		value *int64
	}{
		{
			name:  "goals",
			query: `SELECT COUNT(*) FROM goals WHERE project_id = ?`,
			value: &goals,
		},
		{
			name:  "tasks",
			query: `SELECT COUNT(*) FROM tasks AS t JOIN goals AS g ON g.id = t.goal_id WHERE g.project_id = ?`,
			value: &tasks,
		},
		{
			name:  "open task handoffs",
			query: `SELECT COUNT(*) FROM task_handoffs AS th JOIN tasks AS t ON t.id = th.task_id JOIN goals AS g ON g.id = t.goal_id WHERE g.project_id = ? AND th.completed_report_at IS NULL`,
			value: &openTaskHandoffs,
		},
		{
			name:  "open goal handoffs",
			query: `SELECT COUNT(*) FROM goal_handoffs AS gh JOIN goals AS g ON g.id = gh.goal_id WHERE g.project_id = ? AND gh.completed_report_at IS NULL`,
			value: &openGoalHandoffs,
		},
	}
	for _, item := range queries {
		if err := db.QueryRowContext(ctx, item.query, projectID).Scan(item.value); err != nil {
			t.Fatalf("count %s: %v", item.name, err)
		}
	}
	return goals, tasks, openTaskHandoffs, openGoalHandoffs
}

func measureQueries(t *testing.T, counter *queryCounter, fn func() error) int64 {
	t.Helper()
	counter.reset()
	if err := fn(); err != nil {
		t.Fatalf("measured operation: %v", err)
	}
	return counter.load()
}

func measureDurations(t *testing.T, samples int, fn func() error) []time.Duration {
	t.Helper()
	durations := make([]time.Duration, samples)
	for i := range durations {
		started := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("duration sample %d: %v", i+1, err)
		}
		durations[i] = time.Since(started)
	}
	return durations
}

func measureParallelDurations(t *testing.T, samples int, fn func() error) []time.Duration {
	t.Helper()
	durations := make([]time.Duration, samples)
	start := make(chan struct{})
	var ready sync.WaitGroup
	var workers sync.WaitGroup
	ready.Add(samples)
	workers.Add(samples)
	errs := make(chan error, samples)
	for i := range durations {
		go func(i int) {
			defer workers.Done()
			ready.Done()
			<-start
			started := time.Now()
			err := fn()
			durations[i] = time.Since(started)
			errs <- err
		}(i)
	}
	ready.Wait()
	close(start)
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("parallel duration sample: %v", err)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return durations
}

func durationMillis(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func medianDuration(values []time.Duration) time.Duration {
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	if len(ordered)%2 == 1 {
		return ordered[len(ordered)/2]
	}
	return (ordered[len(ordered)/2-1] + ordered[len(ordered)/2]) / 2
}

func installCountingDB(t *testing.T, s *Store, path string, counter *queryCounter) error {
	t.Helper()
	if err := s.db.Close(); err != nil {
		return err
	}
	baseDriver := &sqlite.Driver{}
	s.db = sql.OpenDB(countingConnector{dsn: path, driver: baseDriver, counter: counter})
	s.db.SetMaxOpenConns(1)
	s.db.SetMaxIdleConns(1)
	if err := configureDatabase(s.db); err != nil {
		_ = s.db.Close()
		return err
	}
	if err := s.db.PingContext(context.Background()); err != nil {
		_ = s.db.Close()
		return err
	}
	return nil
}

type queryCounter struct {
	queries int64
}

func (c *queryCounter) reset() {
	atomic.StoreInt64(&c.queries, 0)
}

func (c *queryCounter) add() {
	atomic.AddInt64(&c.queries, 1)
}

func (c *queryCounter) load() int64 {
	return atomic.LoadInt64(&c.queries)
}

type countingConnector struct {
	dsn     string
	driver  driver.Driver
	counter *queryCounter
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{conn: conn, counter: c.counter}, nil
}

func (c countingConnector) Driver() driver.Driver {
	return c.driver
}

type countingConn struct {
	conn    driver.Conn
	counter *queryCounter
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &countingStmt{stmt: stmt, counter: c.counter}, nil
}

func (c *countingConn) Close() error {
	return c.conn.Close()
}

func (c *countingConn) Begin() (driver.Tx, error) {
	return c.conn.Begin()
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if preparer, ok := c.conn.(driver.ConnPrepareContext); ok {
		stmt, err := preparer.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &countingStmt{stmt: stmt, counter: c.counter}, nil
	}
	return c.Prepare(query)
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := queryer.QueryContext(ctx, query, args)
	if err != driver.ErrSkip {
		c.counter.add()
	}
	return rows, err
}

func (c *countingConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	queryer, ok := c.conn.(driver.Queryer)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := queryer.Query(query, args)
	if err != driver.ErrSkip {
		c.counter.add()
	}
	return rows, err
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, query, args)
}

func (c *countingConn) Exec(query string, args []driver.Value) (driver.Result, error) {
	execer, ok := c.conn.(driver.Execer)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.Exec(query, args)
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.conn.(driver.ConnBeginTx)
	if !ok {
		return nil, driver.ErrSkip
	}
	return beginner.BeginTx(ctx, opts)
}

func (c *countingConn) Ping(ctx context.Context) error {
	pinger, ok := c.conn.(driver.Pinger)
	if !ok {
		return driver.ErrSkip
	}
	return pinger.Ping(ctx)
}

func (c *countingConn) ResetSession(ctx context.Context) error {
	resetter, ok := c.conn.(driver.SessionResetter)
	if !ok {
		return nil
	}
	return resetter.ResetSession(ctx)
}

func (c *countingConn) IsValid() bool {
	validator, ok := c.conn.(driver.Validator)
	return !ok || validator.IsValid()
}

func (c *countingConn) CheckNamedValue(value *driver.NamedValue) error {
	checker, ok := c.conn.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return checker.CheckNamedValue(value)
}

type countingStmt struct {
	stmt    driver.Stmt
	counter *queryCounter
}

func (s *countingStmt) Close() error {
	return s.stmt.Close()
}

func (s *countingStmt) NumInput() int {
	return s.stmt.NumInput()
}

func (s *countingStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.stmt.Exec(args)
}

func (s *countingStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := s.stmt.Query(args)
	if err != driver.ErrSkip {
		s.counter.add()
	}
	return rows, err
}

func (s *countingStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := s.stmt.(driver.StmtExecContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	return execer.ExecContext(ctx, args)
}

func (s *countingStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := s.stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := queryer.QueryContext(ctx, args)
	if err != driver.ErrSkip {
		s.counter.add()
	}
	return rows, err
}

func (s *countingStmt) CheckNamedValue(value *driver.NamedValue) error {
	checker, ok := s.stmt.(driver.NamedValueChecker)
	if !ok {
		return driver.ErrSkip
	}
	return checker.CheckNamedValue(value)
}
