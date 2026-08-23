package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestFormatSessionRoleIncludesEvidence(t *testing.T) {
	got := formatSessionRole(sessionRoleResponse{
		Role:      "commander",
		ProjectID: "project-1",
		GoalID:    "goal-1",
	})
	for _, want := range []string{
		"role: commander",
		"project_id: project-1",
		"goal_id: goal-1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatSessionRole() = %q, want %q", got, want)
		}
	}
}

func TestCheckExpectedRoleAcceptsMatchingRole(t *testing.T) {
	code, message := checkExpectedRole(sessionRoleResponse{
		Role:      "executor",
		ProjectID: "",
		GoalID:    "",
	}, "executor")
	if code != 0 {
		t.Fatalf("matching role exit code = %d, want 0", code)
	}
	if message != "" {
		t.Fatalf("matching role message = %q, want empty", message)
	}
}

func TestCheckExpectedRoleRejectsMismatchWithEvidence(t *testing.T) {
	code, message := checkExpectedRole(sessionRoleResponse{
		Role:      "subcommander",
		ProjectID: "project-1",
		GoalID:    "goal-1",
	}, "executor")
	if code == 0 {
		t.Fatal("mismatching role exit code = 0, want non-zero")
	}
	for _, want := range []string{
		"expected: executor",
		"actual: subcommander",
		"project_id: project-1",
		"goal_id: goal-1",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("mismatch message = %q, want %q", message, want)
		}
	}
}

func TestRoleExpectedValueValidation(t *testing.T) {
	if err := validateExpectedRole("king"); err == nil {
		t.Fatal("validateExpectedRole(king) = nil, want an error")
	}
	for _, role := range []string{"commander", "subcommander", "executor"} {
		if err := validateExpectedRole(role); err != nil {
			t.Fatalf("validateExpectedRole(%q) = %v, want nil", role, err)
		}
	}
}

func TestRunRoleUnregisteredDirectoryIsSilent(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() = %v", err)
	}
	os.Stdout = w
	code, runErr := runRole(cliConfig{}, t.TempDir(), "")
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close stdout pipe: %v", closeErr)
	}
	os.Stdout = oldStdout
	got, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout pipe: %v", readErr)
	}
	if runErr != nil {
		t.Fatalf("runRole() error = %v, want nil", runErr)
	}
	if code != 0 {
		t.Fatalf("runRole() exit code = %d, want 0", code)
	}
	if len(got) != 0 {
		t.Fatalf("runRole() output = %q, want empty", got)
	}
}
