package store

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestResolveCommitReturnsSubjectAndStats(t *testing.T) {
	root := commitTestRepositoryRoot(t)
	sha := strings.TrimSpace(commitTestGitOutput(t, root, "rev-parse", "HEAD"))

	got, err := (&Store{}).ResolveCommit(context.Background(), root, sha)
	if err != nil {
		t.Fatalf("ResolveCommit: %v", err)
	}

	wantSubject := strings.TrimSpace(commitTestGitOutput(t, root, "show", "--no-patch", "--format=%s", sha))
	wantStats := commitTestExpectedNumstat(t, commitTestGitOutput(t, root, "show", "--numstat", "--format=", sha))
	if got.SHA != sha {
		t.Fatalf("SHA = %q, want %q", got.SHA, sha)
	}
	if got.Subject != wantSubject {
		t.Fatalf("Subject = %q, want %q", got.Subject, wantSubject)
	}
	if got.FilesChanged != wantStats.FilesChanged || got.Insertions != wantStats.Insertions || got.Deletions != wantStats.Deletions {
		t.Fatalf("stats = (%d, %d, %d), want (%d, %d, %d)", got.FilesChanged, got.Insertions, got.Deletions, wantStats.FilesChanged, wantStats.Insertions, wantStats.Deletions)
	}
	if !got.CreatedAt.IsZero() {
		t.Fatalf("CreatedAt = %v, want zero", got.CreatedAt)
	}
}

func TestResolveCommitRejectsUnknownSHAWithRecoveryInstruction(t *testing.T) {
	root := commitTestRepositoryRoot(t)
	_, err := (&Store{}).ResolveCommit(context.Background(), root, "0000000000000000000000000000000000000000")
	if err == nil {
		t.Fatal("ResolveCommit: expected error for unknown SHA")
	}
	if !strings.Contains(err.Error(), "git log --oneline -5") {
		t.Fatalf("error = %q, want git log instruction", err)
	}
}

func TestResolveCommitExpandsShortSHA(t *testing.T) {
	root := commitTestRepositoryRoot(t)
	fullSHA := strings.TrimSpace(commitTestGitOutput(t, root, "rev-parse", "HEAD"))
	shortSHA := fullSHA[:7]

	got, err := (&Store{}).ResolveCommit(context.Background(), root, shortSHA)
	if err != nil {
		t.Fatalf("ResolveCommit: %v", err)
	}
	if got.SHA != fullSHA {
		t.Fatalf("SHA = %q, want full SHA %q", got.SHA, fullSHA)
	}
	if len(got.SHA) != 40 {
		t.Fatalf("SHA length = %d, want 40", len(got.SHA))
	}
}

func TestParseCommitNumstatCountsBinaryFilesWithoutLineTotals(t *testing.T) {
	got, err := parseCommitNumstat("2\t3\ttext.txt\n-\t-\timage.bin\n0\t1\tother.go\n")
	if err != nil {
		t.Fatalf("parseCommitNumstat: %v", err)
	}
	if got.FilesChanged != 3 || got.Insertions != 2 || got.Deletions != 4 {
		t.Fatalf("stats = (%d, %d, %d), want (3, 2, 4)", got.FilesChanged, got.Insertions, got.Deletions)
	}
}

func commitTestRepositoryRoot(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(commitTestGitOutput(t, ".", "rev-parse", "--show-toplevel"))
}

func commitTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func commitTestExpectedNumstat(t *testing.T, output string) commitStats {
	t.Helper()
	var stats commitStats
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("unexpected numstat line %q", line)
		}
		stats.FilesChanged++
		if fields[0] != "-" {
			value, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatalf("parse insertions %q: %v", fields[0], err)
			}
			stats.Insertions += value
		}
		if fields[1] != "-" {
			value, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatalf("parse deletions %q: %v", fields[1], err)
			}
			stats.Deletions += value
		}
	}
	return stats
}
