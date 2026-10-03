package daemon

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// next_step exists so an agent does not read doc/execution-flow.md to find its
// next call. That only holds while the two name the same operations, and
// nothing else keeps them together.
//
// This compares the tools each side names, not the shape of the chart. The
// chart has nodes that are not tool calls ("worktree を用意"), collapses
// several hops the table states as one, and puts two tools in one node, so a
// graph comparison would fail on wording changes rather than on drift. What
// does drift is the set: a step added to the flow whose tool never reaches the
// table, or a tool renamed on one side. Order is already covered by the
// execution flow tests, which walk the real transitions.

var atctToolPattern = regexp.MustCompile(`\batct_[a-z_]+`)

func chartTools(t *testing.T) map[string]bool {
	t.Helper()
	doc, err := os.ReadFile("../../doc/execution-flow.md")
	if err != nil {
		t.Fatalf("read doc/execution-flow.md: %v", err)
	}
	text := string(doc)
	start := strings.Index(text, "```mermaid")
	if start < 0 {
		t.Fatal("doc/execution-flow.md has no mermaid chart")
	}
	end := strings.Index(text[start+len("```mermaid"):], "```")
	if end < 0 {
		t.Fatal("doc/execution-flow.md has an unterminated mermaid chart")
	}
	chart := text[start : start+len("```mermaid")+end]

	tools := map[string]bool{}
	for _, name := range atctToolPattern.FindAllString(chart, -1) {
		tools[name] = true
	}
	if len(tools) == 0 {
		t.Fatal("the chart names no atct tools; the extraction is wrong, not the chart")
	}
	return tools
}

func tableTools() map[string]bool {
	tools := map[string]bool{}
	for _, options := range nextStepAfter {
		for _, option := range options {
			tools[option.Call] = true
		}
	}
	return tools
}

func missing(from, in map[string]bool) []string {
	var names []string
	for name := range from {
		if !in[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func TestNextStepNamesEveryToolTheChartDoes(t *testing.T) {
	if got := missing(chartTools(t), tableTools()); len(got) > 0 {
		t.Fatalf("doc/execution-flow.md names %v, which no next_step offers; an agent reaching that step has to read the chart", got)
	}
}

func TestNextStepNamesNoToolOutsideTheChart(t *testing.T) {
	if got := missing(tableTools(), chartTools(t)); len(got) > 0 {
		t.Fatalf("next_step offers %v, which the chart does not name; one of the two is stale", got)
	}
}
