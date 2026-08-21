package store

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/michiomochi/atct/internal/domain"
)

type commitStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

func (s *Store) ResolveCommit(ctx context.Context, projectRoot, sha string) (domain.TaskCommit, error) {
	metadataOutput, err := runCommitGit(ctx, projectRoot, "show", "--no-patch", "--format=%H%x00%s", sha)
	if err != nil {
		return domain.TaskCommit{}, unknownCommitError(sha)
	}
	metadata := strings.TrimSuffix(string(metadataOutput), "\n")
	parts := strings.SplitN(metadata, "\x00", 2)
	if len(parts) != 2 || parts[0] == "" {
		return domain.TaskCommit{}, fmt.Errorf("コミット情報を解析できません: %q", metadata)
	}

	numstatOutput, err := runCommitGit(ctx, projectRoot, "show", "--numstat", "--format=", sha)
	if err != nil {
		return domain.TaskCommit{}, fmt.Errorf("コミット %s の変更統計を取得できません: %w", parts[0], err)
	}
	stats, err := parseCommitNumstat(string(numstatOutput))
	if err != nil {
		return domain.TaskCommit{}, fmt.Errorf("コミット %s の変更統計を解析できません: %w", parts[0], err)
	}

	return domain.TaskCommit{
		SHA:          parts[0],
		Subject:      parts[1],
		FilesChanged: stats.FilesChanged,
		Insertions:   stats.Insertions,
		Deletions:    stats.Deletions,
	}, nil
}

func runCommitGit(ctx context.Context, projectRoot string, args ...string) ([]byte, error) {
	gitArgs := append([]string{"-C", projectRoot}, args...)
	return exec.CommandContext(ctx, "git", gitArgs...).Output()
}

func parseCommitNumstat(output string) (commitStats, error) {
	var stats commitStats
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 {
			return commitStats{}, fmt.Errorf("不正な numstat 行: %q", line)
		}

		stats.FilesChanged++
		if fields[0] != "-" {
			insertions, err := strconv.Atoi(fields[0])
			if err != nil {
				return commitStats{}, fmt.Errorf("追加行数 %q を解析できません: %w", fields[0], err)
			}
			stats.Insertions += insertions
		}
		if fields[1] != "-" {
			deletions, err := strconv.Atoi(fields[1])
			if err != nil {
				return commitStats{}, fmt.Errorf("削除行数 %q を解析できません: %w", fields[1], err)
			}
			stats.Deletions += deletions
		}
	}
	return stats, nil
}

func unknownCommitError(sha string) error {
	return fmt.Errorf("その SHA はこのリポジトリに無い: %s。git log --oneline -5 で確かめてください", sha)
}
