package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

type commanderRole struct {
	Role      string   `json:"role"`
	ProjectID int64    `json:"project_id"`
	Does      []string `json:"does"`
	DoesNot   []string `json:"does_not"`
	UILocale  string   `json:"ui_locale"`
}

// subcommanderRole is the pre-2-layer response of a goal handoff holder; it is
// still decoded so a newer CLI works against an older daemon.
type subcommanderRole struct {
	Role     string   `json:"role"`
	GoalID   int64    `json:"goal_id"`
	Does     []string `json:"does"`
	DoesNot  []string `json:"does_not"`
	UILocale string   `json:"ui_locale"`
}

// goalExecutorRole is the visible "executor": a session holding a goal handoff.
type goalExecutorRole struct {
	Role     string   `json:"role"`
	GoalID   int64    `json:"goal_id"`
	Does     []string `json:"does"`
	DoesNot  []string `json:"does_not"`
	UILocale string   `json:"ui_locale"`
}

// executorRole carries every response without a goal: "task_worker", "none",
// and the legacy "executor" of an older daemon.
type executorRole struct {
	Role     string   `json:"role"`
	Does     []string `json:"does"`
	DoesNot  []string `json:"does_not"`
	UILocale string   `json:"ui_locale"`
}

type roleResponse interface {
	roleName() string
}

func (r commanderRole) roleName() string    { return r.Role }
func (r subcommanderRole) roleName() string { return r.Role }
func (r goalExecutorRole) roleName() string { return r.Role }
func (r executorRole) roleName() string     { return r.Role }

func decodeSessionRole(raw json.RawMessage) (roleResponse, error) {
	var discriminator struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return nil, fmt.Errorf("decode session.role discriminator: %w", err)
	}
	switch discriminator.Role {
	case "commander":
		var response commanderRole
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode commander role: %w", err)
		}
		return response, nil
	case "subcommander":
		var response subcommanderRole
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode subcommander role: %w", err)
		}
		return response, nil
	case "executor":
		// goal_id tells the new executor (a goal handoff holder) from the legacy
		// task-holding "executor" of an older daemon.
		var goal goalExecutorRole
		if err := json.Unmarshal(raw, &goal); err != nil {
			return nil, fmt.Errorf("decode executor role: %w", err)
		}
		if goal.GoalID != 0 {
			return goal, nil
		}
		var response executorRole
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode executor role: %w", err)
		}
		return response, nil
	case "task_worker", "none":
		var response executorRole
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("decode %s role: %w", discriminator.Role, err)
		}
		return response, nil
	default:
		return nil, fmt.Errorf("decode session.role: unknown role %q", discriminator.Role)
	}
}

var validSessionRoles = map[string]struct{}{
	"commander":    {},
	"executor":     {},
	"subcommander": {},
	"task_worker":  {},
}

func formatSessionRole(response roleResponse) string {
	switch response := response.(type) {
	case commanderRole:
		return fmt.Sprintf("role: %s\nproject_id: %d\n", response.Role, response.ProjectID)
	case subcommanderRole:
		return fmt.Sprintf("role: %s\ngoal_id: %d\n", response.Role, response.GoalID)
	case goalExecutorRole:
		return fmt.Sprintf("role: %s\ngoal_id: %d\n", response.Role, response.GoalID)
	case executorRole:
		return fmt.Sprintf("role: %s\n", response.Role)
	default:
		return ""
	}
}

func validateExpectedRole(expected string) error {
	if _, ok := validSessionRoles[expected]; ok {
		return nil
	}
	// An unknown expectation is a command-line error, not a role mismatch:
	// treating a typo such as "king" as a real role would turn a bad guard into
	// a misleading, valid-looking negative check.
	return fmt.Errorf("invalid expected role %q (choose commander or executor)", expected)
}

// roleMatches reports whether a role response satisfies an expected role.
// "executor" means a goal handoff holder (goal_id != 0); it also accepts a
// task_worker, so a worker started before the 2-layer change can re-check its
// role after a context compaction. "subcommander" is the old name of a goal
// handoff holder. "none" matches nothing, so a session that lost its handoff
// does not pass a role check.
func roleMatches(response roleResponse, expected string) bool {
	switch expected {
	case "commander":
		_, ok := response.(commanderRole)
		return ok
	case "executor":
		switch response.(type) {
		case goalExecutorRole, subcommanderRole:
			return true
		case executorRole:
			// A bare {"role":"executor"} without goal_id is an older daemon's
			// task holder; it cannot be told from a session holding nothing.
			return response.roleName() == "task_worker"
		}
	case "subcommander":
		switch response.(type) {
		case goalExecutorRole, subcommanderRole:
			return true
		}
	case "task_worker":
		return response.roleName() == "task_worker"
	}
	return false
}

func checkExpectedRole(response roleResponse, expected string) (int, string) {
	if expected == "" || roleMatches(response, expected) {
		return 0, ""
	}
	switch response := response.(type) {
	case commanderRole:
		return 1, fmt.Sprintf("role mismatch: expected: %s; actual: %s; project_id: %d", expected, response.Role, response.ProjectID)
	case subcommanderRole:
		return 1, fmt.Sprintf("role mismatch: expected: %s; actual: %s; goal_id: %d", expected, response.Role, response.GoalID)
	case goalExecutorRole:
		return 1, fmt.Sprintf("role mismatch: expected: %s; actual: %s; goal_id: %d", expected, response.Role, response.GoalID)
	case executorRole:
		return 1, fmt.Sprintf("role mismatch: expected: %s; actual: %s", expected, response.Role)
	default:
		return 1, fmt.Sprintf("role mismatch: expected: %s", expected)
	}
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
	agentSessionID, err := strconv.ParseInt(sessionID, 10, 64)
	if err != nil || agentSessionID <= 0 {
		return 2, fmt.Errorf("role requires a positive numeric --agent-session-id")
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
	var raw json.RawMessage
	if err := client.Call(context.Background(), "session.role", map[string]any{
		"agent_session_id": agentSessionID,
	}, &raw); err != nil {
		return 1, fmt.Errorf("session.role: %w", err)
	}
	response, err := decodeSessionRole(raw)
	if err != nil {
		return 1, fmt.Errorf("session.role: %w", err)
	}

	fmt.Fprint(os.Stdout, formatSessionRole(response))
	code, message := checkExpectedRole(response, config.roleExpected)
	if message != "" {
		fmt.Fprintln(os.Stderr, message)
	}
	return code, nil
}
