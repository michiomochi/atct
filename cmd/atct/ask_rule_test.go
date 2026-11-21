package main

import (
	"strings"
	"testing"
)

func assertContainsAll(t *testing.T, rel, text string, want []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(text, s) {
			t.Errorf("%s lacks %q", rel, s)
		}
	}
}

func assertContainsNone(t *testing.T, rel, text string, unwanted []string) {
	t.Helper()
	for _, s := range unwanted {
		if strings.Contains(text, s) {
			t.Errorf("%s still contains %q", rel, s)
		}
	}
}

func TestAskRuleAtctSkill(t *testing.T) {
	rel := "skills/atct/SKILL.md"
	skill := readRepoText(t, rel)
	assertContainsAll(t, rel, skill, []string{
		"Ask the human only when one of these holds",
		"cannot be undone or is destructive",
		"only the human can do",
		"has not already approved",
		"asked for or approved",
		"do not call `atct_decision_ask`",
		"10 stocks",
		"4, 4 and 2",
		"not a new cost to ask about",
	})
	assertContainsNone(t, rel, skill, []string{"ask instead of guessing"})
}

func TestAskRuleStartSkill(t *testing.T) {
	rel := "skills/start/SKILL.md"
	skill := readRepoText(t, rel)
	assertContainsAll(t, rel, skill, []string{
		"Four times, and no others",
		"only the human can do",
		"not yet approved",
	})
	assertContainsNone(t, rel, skill, []string{
		"Three times, and no others",
		"cannot settle it from the code",
	})
}

// The subcommander skill was merged into atct:executor; it only points there.
func TestSubcommanderSkillPointsToExecutor(t *testing.T) {
	rel := "skills/subcommander/SKILL.md"
	skill := readRepoText(t, rel)
	assertContainsAll(t, rel, skill, []string{"atct:executor"})
}

func TestAskRuleExecutorSkill(t *testing.T) {
	rel := "skills/executor/SKILL.md"
	skill := readRepoText(t, rel)
	assertContainsAll(t, rel, skill, []string{
		"An executor's decision request",
		"only in the cases `atct:atct`",
		"decide a reversible,",
		"non-destructive choice of means yourself",
		"cannot call `atct_decision_ask`",
	})
	assertContainsNone(t, rel, skill, []string{
		"Return design or irreversible decisions to the delegator",
	})
}
