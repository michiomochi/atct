package script

import (
	"os"
	"testing"
)

func TestWorktreeReclaimEmbedsFile(t *testing.T) {
	want, err := os.ReadFile("worktree-reclaim.sh")
	if err != nil {
		t.Fatal(err)
	}
	if WorktreeReclaim == "" {
		t.Fatal("WorktreeReclaim is empty")
	}
	if WorktreeReclaim != string(want) {
		t.Fatal("WorktreeReclaim differs from worktree-reclaim.sh")
	}
}
