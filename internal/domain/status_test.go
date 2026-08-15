package domain

import "testing"

func TestParseTaskStatus(t *testing.T) {
	got, err := ParseTaskStatus("doing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != TaskDoing {
		t.Fatalf("got %q, want %q", got, TaskDoing)
	}
}

func TestParseTaskStatusRejectsUnknown(t *testing.T) {
	if _, err := ParseTaskStatus("waiting_decision"); err == nil {
		t.Fatal("expected error for unknown status, got nil")
	}
}
