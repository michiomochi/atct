package doc

import (
	"os"
	"testing"
)

func TestExecutionFlowEmbedsFile(t *testing.T) {
	want, err := os.ReadFile("execution-flow.md")
	if err != nil {
		t.Fatal(err)
	}
	if ExecutionFlow == "" {
		t.Fatal("ExecutionFlow is empty")
	}
	if ExecutionFlow != string(want) {
		t.Fatal("ExecutionFlow differs from execution-flow.md")
	}
}
