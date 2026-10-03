package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var specPlanReferenceCases = []struct {
	name    string
	text    string
	refOnly bool
}{
	{"plans path", "詳細は doc/plans/x.md", true},
	{"superpowers path", "docs/superpowers/specs/x.md を参照", true},
	{"path with long body", "doc/specs/x.md\n" + strings.Repeat("あ", 200), false},
	{"plans path with trailing japanese", "詳細は doc/plans/x.md を参照", true},
	{"path then long japanese on same line", "doc/specs/x.md" + strings.Repeat("あ", 300), false},
	{"backquoted path only", "`doc/plans/x.md`", true},
	{"empty", "", false},
	{"short text without path", "# Spec", false},
}

func TestSpecPlanReferenceOnly(t *testing.T) {
	for _, tc := range specPlanReferenceCases {
		if got := specPlanReferenceOnly(tc.text); got != tc.refOnly {
			t.Errorf("%s: specPlanReferenceOnly(%q) = %v, want %v", tc.name, tc.text, got, tc.refOnly)
		}
	}
}

func TestUpdateGoalRequestReportRefusesReferenceOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	for _, tc := range specPlanReferenceCases {
		for _, field := range []string{"spec", "plan"} {
			spec, plan := "# Spec", "# Plan"
			if field == "spec" {
				spec = tc.text
			} else {
				plan = tc.text
			}
			_, err := s.UpdateGoalRequestReport(ctx, goalID, spec, plan)
			if got := errors.Is(err, ErrSpecPlanReferenceOnly); got != tc.refOnly {
				t.Errorf("%s/%s: err = %v, want reference-only=%v", tc.name, field, err, tc.refOnly)
			}
			if !tc.refOnly && err != nil {
				t.Errorf("%s/%s: unexpected error %v", tc.name, field, err)
			}
		}
	}
}

func TestRequestPlanHandoffReviewRefusesReferenceOnly(t *testing.T) {
	for _, tc := range specPlanReferenceCases {
		for _, field := range []string{"spec", "plan"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				s := newTestStore(t)
				ctx := context.Background()
				goalID := newTestGoal(t, s)
				addLiveProjectClaim(t, s, goalID, "ref-commander")
				addTestAgentSession(t, s, "ref-subcommander")
				gh, err := s.RequestGoalHandoff(ctx, "ref-goal", goalID, testSessionID("ref-commander"), "delegate")
				if err != nil {
					t.Fatal(err)
				}
				subID := testSessionID("ref-subcommander")
				if _, err := s.ReceiveGoalHandoff(ctx, gh.ID, goalID, subID); err != nil {
					t.Fatal(err)
				}
				spec, plan := "# Spec", "# Plan"
				if field == "spec" {
					spec = tc.text
				} else {
					plan = tc.text
				}
				// Write directly: UpdateGoalRequestReport now refuses reference-only text, but legacy rows may hold it.
				if _, err := s.db.ExecContext(ctx, `UPDATE goals SET spec = ?, plan = ? WHERE id = ?`, spec, plan, goalID); err != nil {
					t.Fatal(err)
				}
				_, err = s.RequestPlanHandoffReview(ctx, "ref-review", goalID, subID, "ready")
				if tc.text == "" {
					if !errors.Is(err, ErrPlanHandoffGoalArtifactsEmpty) {
						t.Fatalf("err = %v, want ErrPlanHandoffGoalArtifactsEmpty", err)
					}
					return
				}
				if got := errors.Is(err, ErrSpecPlanReferenceOnly); got != tc.refOnly {
					t.Fatalf("err = %v, want reference-only=%v", err, tc.refOnly)
				}
				if !tc.refOnly && err != nil {
					t.Fatalf("unexpected error %v", err)
				}
			})
		}
	}
}
