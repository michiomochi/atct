package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/michiomochi/atct/internal/store"
)

// claimCheckCommand reports whether every given task is claimed by an agent
// session that is still running. It exits non-zero when any of them is not,
// which is what lets a shell script refuse to delegate work nobody has claimed.
//
// Claiming itself stays in MCP: a claim belongs to an agent session, and a CLI
// process would hold one only for the moment it takes to exit. So this reads,
// and the caller is expected to have claimed through the tools first.
func claimCheckCommand(dir string, args []string) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 2, fmt.Errorf("getwd: %w", err)
	}

	// "any" asks a different question: is anything at all being worked on in this
	// project? The delegation hook needs that, because it sees a request file and
	// not the task ids inside it. It is a bare word rather than a flag because the
	// flag parser owns everything starting with a dash.
	anyClaim := false
	taskIDs := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "any" {
			anyClaim = true
			continue
		}
		taskIDs = append(taskIDs, arg)
	}
	if !anyClaim && len(taskIDs) == 0 {
		return 2, fmt.Errorf(`claim-check needs at least one task_id, or the word "any"`)
	}

	dbPath := filepath.Join(dir, "atct.db")
	if _, statErr := os.Stat(dbPath); statErr != nil {
		return 2, fmt.Errorf("stat store: %w", statErr)
	}
	s, err := store.Open(dbPath)
	if err != nil {
		return 2, fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx := context.Background()
	project, err := resolveProjectSelection(ctx, s, cwd, "", false)
	if err != nil {
		return 2, fmt.Errorf("resolve project: %w", err)
	}

	running, _, err := store.ClaimLiveness(ctx, s, project.ID)
	if err != nil {
		return 2, fmt.Errorf("claim liveness: %w", err)
	}
	live := make(map[int64]struct{}, len(running))
	for _, task := range running {
		live[task.ID] = struct{}{}
	}

	if anyClaim {
		if len(running) == 0 {
			fmt.Fprintln(os.Stderr, "no task in this project is claimed by a running agent session")
			return 1, nil
		}
		return 0, nil
	}

	missing := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		canonicalID, err := s.ResolveTaskID(ctx, id)
		if err != nil {
			missing = append(missing, id)
			continue
		}
		if _, ok := live[canonicalID]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		for _, id := range missing {
			fmt.Fprintf(os.Stderr, "not claimed by a running agent session: %s\n", id)
		}
		return 1, nil
	}
	return 0, nil
}
