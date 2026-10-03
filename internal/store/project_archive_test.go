package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

type archiveFixture struct {
	s        *Store
	archived domain.Project
	other    domain.Project
	goalA    domain.Goal // in the project that gets archived
	goalB    domain.Goal // in the other project
	decA     domain.Decision
	decB     domain.Decision
}

func newArchiveFixture(t *testing.T) archiveFixture {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	f := archiveFixture{s: s}
	var err error
	if f.archived, err = s.CreateProject(ctx, "arch", "/repos/arch"); err != nil {
		t.Fatal(err)
	}
	if f.other, err = s.CreateProject(ctx, "other", "/repos/other"); err != nil {
		t.Fatal(err)
	}
	ask := func(goal domain.Goal, key string) domain.Decision {
		after := int64(1000)
		tasks, err := s.CreateTasks(ctx, goal.ID, "agent", key, []string{"t"}, []string{"d"})
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.AskDecision(ctx, AskInput{
			GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "q",
			Options: []domain.Option{{Label: "A"}, {Label: "B"}}, DefaultOption: "A", DefaultAfterMs: &after,
		})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if f.goalA, err = s.CreateGoal(ctx, f.archived.ID, "goal a", "human"); err != nil {
		t.Fatal(err)
	}
	if f.goalB, err = s.CreateGoal(ctx, f.other.ID, "goal b", "human"); err != nil {
		t.Fatal(err)
	}
	f.decA, f.decB = ask(f.goalA, "ka"), ask(f.goalB, "kb")
	return f
}

func contains(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestArchiveHidesProjectFromReadsAndUnarchiveRestores(t *testing.T) {
	ctx := context.Background()
	f := newArchiveFixture(t)
	s := f.s

	// Answer both decisions so the unapplied-list queries have rows.
	for _, d := range []domain.Decision{f.decA, f.decB} {
		if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerLabel: "A"}); err != nil {
			t.Fatalf("AnswerDecision: %v", err)
		}
	}
	// Re-ask open ones for the open/expired lists.
	openA := newOpenDecision(t, s, f.goalA)
	openB := newOpenDecision(t, s, f.goalB)

	check := func(label string, wantArchivedVisible bool) {
		t.Helper()
		listGoals := func(pid int64) []int64 {
			gs, err := s.ListGoals(ctx, pid)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for _, g := range gs {
				ids = append(ids, g.ID)
			}
			return ids
		}
		allGoals, err := s.ListAllGoals(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var allGoalIDs []int64
		for _, g := range allGoals {
			allGoalIDs = append(allGoalIDs, g.ID)
		}
		ids := func(ds []domain.Decision, err error) []int64 {
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, d := range ds {
				out = append(out, d.ID)
			}
			return out
		}
		cases := []struct {
			name     string
			got      []int64
			archived int64
			other    int64
		}{
			{"ListGoals", listGoals(f.archived.ID), f.goalA.ID, 0},
			{"ListAllGoals", allGoalIDs, f.goalA.ID, f.goalB.ID},
			{"ListAllOpenDecisions", ids(s.ListAllOpenDecisions(ctx)), openA.ID, openB.ID},
			{"ListUnappliedDecisions", ids(s.ListUnappliedDecisions(ctx)), f.decA.ID, f.decB.ID},
			{"ListUnappliedDecisionsForProject", ids(s.ListUnappliedDecisionsForProject(ctx, f.archived.ID)), f.decA.ID, 0},
		}
		for _, c := range cases {
			if got := contains(c.got, c.archived); got != wantArchivedVisible {
				t.Errorf("%s/%s: archived row visible = %v, want %v (got %v)", label, c.name, got, wantArchivedVisible, c.got)
			}
			if c.other != 0 && !contains(c.got, c.other) {
				t.Errorf("%s/%s: other project's row missing (got %v)", label, c.name, c.got)
			}
		}
	}

	check("before", true)
	if _, err := s.ArchiveProject(ctx, f.archived.ID); err != nil {
		t.Fatal(err)
	}
	check("archived", false)

	// ID-direct reads still work.
	if _, err := s.GetGoal(ctx, f.goalA.ID); err != nil {
		t.Fatalf("GetGoal on archived: %v", err)
	}
	if ds, err := s.ListOpenDecisions(ctx, f.goalA.ID); err != nil || !decisionIDs(ds)[openA.ID] {
		t.Fatalf("ListOpenDecisions on archived = %v, %v", ds, err)
	}

	if _, err := s.UnarchiveProject(ctx, f.archived.ID); err != nil {
		t.Fatal(err)
	}
	check("unarchived", true)
}

func decisionIDs(ds []domain.Decision) map[int64]bool {
	m := map[int64]bool{}
	for _, d := range ds {
		m[d.ID] = true
	}
	return m
}

func newOpenDecision(t *testing.T, s *Store, goal domain.Goal) domain.Decision {
	t.Helper()
	after := int64(1000)
	tasks, err := s.CreateTasks(context.Background(), goal.ID, "agent", "open-"+goal.Content, []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.AskDecision(context.Background(), AskInput{
		GoalID: goal.ID, TaskID: tasks[0].ID, Kind: domain.KindDecision, Question: "q",
		Options: []domain.Option{{Label: "A"}, {Label: "B"}}, DefaultOption: "A", DefaultAfterMs: &after,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestApplyExpiredDefaultsSkipsArchivedProject(t *testing.T) {
	ctx := context.Background()
	f := newArchiveFixture(t)
	s := f.s
	later := f.decA.CreatedAt.Add(time.Minute)

	if _, err := s.ArchiveProject(ctx, f.archived.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ApplyExpiredDefaults(ctx, later); err != nil || n != 1 {
		t.Fatalf("ApplyExpiredDefaults while archived = %d, %v; want 1 (other project only)", n, err)
	}
	if d, _ := s.GetDecision(ctx, f.decA.ID); d.Status != domain.DecisionOpen {
		t.Fatalf("archived decision status = %q, want open", d.Status)
	}

	if _, err := s.UnarchiveProject(ctx, f.archived.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ApplyExpiredDefaults(ctx, later); err != nil || n != 1 {
		t.Fatalf("ApplyExpiredDefaults after unarchive = %d, %v; want 1", n, err)
	}
	if d, _ := s.GetDecision(ctx, f.decA.ID); d.Status != domain.DecisionAnswered {
		t.Fatalf("decision status after unarchive = %q, want answered", d.Status)
	}
}

func TestArchiveProjectIdempotentAndKeepsClaim(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, err := s.CreateProject(ctx, "p", "/repos/p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE projects SET claimed_by = 7, claimed_at = '2026-01-01T00:00:00Z' WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}

	first, err := s.ArchiveProject(ctx, p.ID)
	if err != nil || first.ArchivedAt == nil {
		t.Fatalf("ArchiveProject = %+v, %v", first, err)
	}
	if first.ClaimedBy != 7 || first.ClaimedAt == nil {
		t.Fatalf("claim changed by archive: %+v", first)
	}
	time.Sleep(1100 * time.Millisecond)
	second, err := s.ArchiveProject(ctx, p.ID)
	if err != nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("second archive = %+v, %v; want original archived_at %v", second, err, first.ArchivedAt)
	}

	un, err := s.UnarchiveProject(ctx, p.ID)
	if err != nil || un.ArchivedAt != nil || un.ClaimedBy != 7 {
		t.Fatalf("UnarchiveProject = %+v, %v", un, err)
	}
	if again, err := s.UnarchiveProject(ctx, p.ID); err != nil || again.ArchivedAt != nil {
		t.Fatalf("second unarchive = %+v, %v", again, err)
	}

	if _, err := s.ArchiveProject(ctx, 9999); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("ArchiveProject missing = %v, want ErrProjectNotFound", err)
	}
	if _, err := s.UnarchiveProject(ctx, 9999); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("UnarchiveProject missing = %v, want ErrProjectNotFound", err)
	}
}

func TestEnsureProjectActive(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	p, err := s.CreateProject(ctx, "proj-x", "/repos/x")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProjectActive(ctx, p.ID); err != nil {
		t.Fatalf("active project: %v", err)
	}
	if _, err := s.ArchiveProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	err = s.EnsureProjectActive(ctx, p.ID)
	if !errors.Is(err, ErrProjectArchived) {
		t.Fatalf("err = %v, want ErrProjectArchived", err)
	}
	for _, want := range []string{"proj-x", "atct project unarchive proj-x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if err := s.EnsureProjectActive(ctx, 9999); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project err = %v, want ErrProjectNotFound", err)
	}
}

func TestMigration0053KeepsExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "atct.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, "old", "/repos/old")
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to the 0052 state.
	for _, q := range []string{
		`ALTER TABLE projects DROP COLUMN archived_at`,
		`DELETE FROM schema_migrations WHERE filename = '0053_project_archive.sql'`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen at 0052: %v", err)
	}
	defer s.Close()
	projects, err := s.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].ID != p.ID || projects[0].ArchivedAt != nil {
		t.Fatalf("projects after migration = %+v, %v", projects, err)
	}
}
