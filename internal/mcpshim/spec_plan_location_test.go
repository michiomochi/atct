package mcpshim

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSpecAndPlanLiveInGoalFieldsIsDistributed(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"atct skill", readRepoFile(t, "skills/atct/SKILL.md"), []string{"Spec and plan live in the goal", "doc/specs", "doc/plans", "docs/superpowers", "`spec` / `plan`", "atct_goal_update_request_report", "superpowers", "reference"}},
		{"subcommander skill", readRepoFile(t, "skills/subcommander/SKILL.md"), []string{"`spec`", "`plan`", "never into files"}},
		{"executor skill", readRepoFile(t, "skills/executor/SKILL.md"), []string{"doc/specs", "doc/plans", "docs/superpowers"}},
		{"execution-flow", readRepoFile(t, "doc/execution-flow.md"), []string{"spec / plan の置き場所", "doc/specs", "doc/plans", "docs/superpowers", "`spec` / `plan`", "superpowers", "個人設定"}},
	}
	for _, tc := range cases {
		for _, w := range tc.want {
			if !strings.Contains(tc.text, w) {
				t.Errorf("%s lacks %q", tc.name, w)
			}
		}
	}
	if !strings.Contains(readRepoFile(t, "skills/atct/SKILL.md"), "| the design and why | the goal's `spec` and `plan` fields, in full, and `work_done` |") {
		t.Error("atct skill table row for the design not updated")
	}
}

func TestGoalUpdateRequestReportDescriptionStatesTheRule(t *testing.T) {
	src := readRepoFile(t, "internal/mcpshim/tools.go")
	i := strings.Index(src, `Name: "atct_goal_update_request_report"`)
	if i < 0 {
		t.Fatal("tool not found")
	}
	line := src[i : i+strings.Index(src[i:], "\n")]
	for _, w := range []string{"in full", "doc/specs", "doc/plans", "docs/superpowers", "refused"} {
		if !strings.Contains(line, w) {
			t.Errorf("description lacks %q", w)
		}
	}
}
