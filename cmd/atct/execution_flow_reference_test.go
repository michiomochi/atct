package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The execution flow and the skills are read from other projects too, where a
// path into the atct repository cannot be opened or run.

func repoRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

func readRepoText(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestATCTSkillCarriesTheSessionStartDirective(t *testing.T) {
	skill := readRepoText(t, "skills/atct/SKILL.md")
	directive := strings.TrimSpace(sessionStartExecutionFlowDirective)
	if !strings.Contains(skill, directive) {
		t.Fatalf("skills/atct/SKILL.md lacks the SessionStart directive %q", directive)
	}
	if strings.Contains(skill, "Read `doc/execution-flow.md`") {
		t.Fatal("skills/atct/SKILL.md still points at doc/execution-flow.md")
	}
}

func TestExecutionFlowAndSkillsHaveNoRepoOnlyReferences(t *testing.T) {
	root := repoRoot(t)
	files := []string{"doc/execution-flow.md"}
	for _, pattern := range []string{"skills/*/SKILL.md", "skills/*/*/SKILL.md"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			rel, err := filepath.Rel(root, m)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, rel)
		}
	}
	for _, rel := range files {
		text := readRepoText(t, rel)
		for _, ref := range []string{"doc/continuous-execution.md", "skills/atct/SKILL.md", "script/worktree-reclaim.sh"} {
			if strings.Contains(text, ref) {
				t.Errorf("%s references %s, which does not resolve outside the atct repository", rel, ref)
			}
		}
	}
}

func TestCommanderMarksRepoOnlyWorktreeScripts(t *testing.T) {
	skill := readRepoText(t, "skills/commander/SKILL.md")
	for _, paragraph := range strings.Split(skill, "\n\n") {
		if (strings.Contains(paragraph, "script/worktree-setup.sh") || strings.Contains(paragraph, "script/worktree-node-modules.sh")) &&
			!strings.Contains(paragraph, "atct repository") {
			t.Errorf("paragraph names an atct-only script without saying so:\n%s", paragraph)
		}
	}
}
