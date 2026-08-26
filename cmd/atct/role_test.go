package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestSessionRoleDecodesNumericEntityIDs(t *testing.T) {
	response, err := decodeSessionRole(json.RawMessage(`{"role":"commander","project_id":21,"does":[],"does_not":[]}`))
	if err != nil {
		t.Fatalf("decodeSessionRole: %v", err)
	}
	commander, ok := response.(commanderRole)
	if !ok {
		t.Fatalf("decoded response = %T, want commanderRole", response)
	}
	if commander.ProjectID != 21 {
		t.Fatalf("decoded project ID = %d, want 21", commander.ProjectID)
	}
}

func TestSessionRoleDecodesRoleSpecificTypes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "commander", raw: `{"role":"commander","project_id":21}`, want: "commander"},
		{name: "subcommander", raw: `{"role":"subcommander","goal_id":22}`, want: "subcommander"},
		{name: "executor", raw: `{"role":"executor"}`, want: "executor"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := decodeSessionRole(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatalf("decodeSessionRole: %v", err)
			}
			switch tt.want {
			case "commander":
				if _, ok := response.(commanderRole); !ok {
					t.Fatalf("decoded response = %T, want commanderRole", response)
				}
			case "subcommander":
				if _, ok := response.(subcommanderRole); !ok {
					t.Fatalf("decoded response = %T, want subcommanderRole", response)
				}
			case "executor":
				if _, ok := response.(executorRole); !ok {
					t.Fatalf("decoded response = %T, want executorRole", response)
				}
			}
		})
	}
}

func TestFormatSessionRoleIncludesEvidence(t *testing.T) {
	got := formatSessionRole(commanderRole{
		Role:      "commander",
		ProjectID: 1,
	})
	for _, want := range []string{
		"role: commander",
		"project_id: 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatSessionRole() = %q, want %q", got, want)
		}
	}
}

func TestCheckExpectedRoleAcceptsMatchingRole(t *testing.T) {
	code, message := checkExpectedRole(executorRole{
		Role: "executor",
	}, "executor")
	if code != 0 {
		t.Fatalf("matching role exit code = %d, want 0", code)
	}
	if message != "" {
		t.Fatalf("matching role message = %q, want empty", message)
	}
}

func TestCheckExpectedRoleRejectsMismatchWithEvidence(t *testing.T) {
	code, message := checkExpectedRole(subcommanderRole{
		Role:   "subcommander",
		GoalID: 1,
	}, "executor")
	if code == 0 {
		t.Fatal("mismatching role exit code = 0, want non-zero")
	}
	for _, want := range []string{
		"expected: executor",
		"actual: subcommander",
		"goal_id: 1",
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
