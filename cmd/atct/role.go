package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

type sessionRoleResponse struct {
	Role      string `json:"role"`
	ProjectID string `json:"project_id"`
	GoalID    string `json:"goal_id"`
}

var validSessionRoles = map[string]struct{}{
	"commander":    {},
	"subcommander": {},
	"executor":     {},
}

func formatSessionRole(response sessionRoleResponse) string {
	return fmt.Sprintf(
		"role: %s\nproject_id: %s\ngoal_id: %s\n",
		response.Role, response.ProjectID, response.GoalID,
	)
}

func validateExpectedRole(expected string) error {
	if _, ok := validSessionRoles[expected]; ok {
		return nil
	}
	// An unknown expectation is a command-line error, not a role mismatch:
	// treating a typo such as "king" as a real role would turn a bad guard into
	// a misleading, valid-looking negative check.
	return fmt.Errorf("invalid expected role %q (choose commander, subcommander, or executor)", expected)
}

func checkExpectedRole(response sessionRoleResponse, expected string) (int, string) {
	if expected == "" || response.Role == expected {
		return 0, ""
	}
	return 1, fmt.Sprintf(
		"role mismatch: expected: %s; actual: %s; project_id: %s; goal_id: %s",
		expected, response.Role, response.ProjectID, response.GoalID,
	)
}

func roleProjectRegistered(dir, cwd string) (bool, error) {
	dbPath := filepath.Join(dir, "atct.db")
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat store: %w", err)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		return false, fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	_, err = resolveProjectSelection(context.Background(), s, cwd, "", false)
	if errors.Is(err, store.ErrProjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolve project: %w", err)
	}
	return true, nil
}

func runRole(config cliConfig, dir, exePath string) (int, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return 1, fmt.Errorf("resolve current directory: %w", err)
	}

	registered, err := roleProjectRegistered(dir, cwd)
	if err != nil {
		return 1, err
	}
	if !registered {
		// This command is also used in arbitrary worktrees. Without a local
		// project record there is no role to inspect, so it is deliberately a
		// silent successful no-op and never starts the daemon.
		return 0, nil
	}

	// The existing CLI paths do not carry an agent_session_id. Do not guess it
	// from the process or an environment variable: either can identify a
	// different session and make a valid claim look like executor work. The
	// caller supplies this stable identity explicitly for the RPC request.
	sessionID := strings.TrimSpace(config.roleAgentSessionID)
	if sessionID == "" {
		return 2, errors.New("role requires --agent-session-id for a registered project")
	}

	// session.role is an RPC and the daemon is the authority for claim-derived
	// roles. A registered project therefore fails closed when the daemon cannot
	// be ensured; only an unregistered directory gets the silent fallback above.
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return 1, fmt.Errorf("ensure daemon: %w", err)
	}

	client := mcpshim.NewClient(reg.SocketPath)
	var response sessionRoleResponse
	if err := client.Call(context.Background(), "session.role", map[string]string{
		"agent_session_id": sessionID,
	}, &response); err != nil {
		return 1, fmt.Errorf("session.role: %w", err)
	}

	fmt.Fprint(os.Stdout, formatSessionRole(response))
	code, message := checkExpectedRole(response, config.roleExpected)
	if message != "" {
		fmt.Fprintln(os.Stderr, message)
	}
	return code, nil
}
