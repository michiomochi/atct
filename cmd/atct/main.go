package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

const (
	defaultListenAddr = "127.0.0.1:8787"
	defaultListenPort = 8787
	// This range is for watch URL discovery, not daemon bind fallback.
	lastListenPort = 8796
)

var listenTCP = net.Listen

type cliConfig struct {
	subcommand              string
	daemonAction            string
	listenAddr              string
	listenExplicit          bool
	contextBrief            bool
	contextCheck            bool
	handoffAction           string
	handoffScope            string
	handoffID               string
	handoffTaskID           string
	handoffGoalID           string
	handoffEntryKind        string
	handoffEntryBody        string
	handoffInReplyToID      int64
	handoffAfterID          int64
	handoffLimit            int
	handoffCompleteReport   string
	handoffCapability       string
	handoffAgentSessionID   string
	projectSpecified        bool
	projectAction           string
	projectName             string
	goalAction              string
	goalTitle               string
	goalDescription         string
	roleExpected            string
	roleExpectedSet         bool
	roleAgentSessionID      string
	stopCheckHookInput      bool
	monitorCheckHookInput   bool
	mergeCheckHookInput     bool
	sessionKeyHookInput     bool
	watchGoalID             string
	watchProjectScope       bool
	watchMonitor            bool
	watchMonitorToken       string
	watchOnce               bool
	codexShimAction         string
	codexShimProfile        string
	codexMonitorAction      string
	codexArgs               []string
	codexMonitorPassthrough bool
	codexMonitorExplicit    bool
	codexMonitorAutomatic   bool
	codexMonitorRole        string
	codexMonitorGoalID      string
	codexMonitorHandoffID   string
}

type cliHandoffEntry struct {
	ID              int64     `json:"id"`
	HandoffID       string    `json:"handoff_id"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	InReplyToID     *int64    `json:"in_reply_to_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type cliHandoffEntryPage struct {
	Entries     []cliHandoffEntry `json:"entries"`
	HasMore     bool              `json:"has_more"`
	NextAfterID int64             `json:"next_after_id"`
}

var errInvalidArgs = errors.New("invalid command line")

var validSubcommands = map[string]bool{
	"daemon":        true,
	"project":       true,
	"goal":          true,
	"context":       true,
	"pending":       true,
	"watch":         true,
	"role":          true,
	"stop-check":    true,
	"monitor-check": true,
	"merge-check":   true,
	"session-key":   true,
	"handoff":       true,
	"codex":         true,
	"version":       true,
}

var validDaemonActions = map[string]bool{"start": true, "stop": true}
var validProjectActions = map[string]bool{"add": true, "list": true}
var validGoalActions = map[string]bool{"add": true, "list": true}
var validHandoffActions = map[string]bool{"append": true, "complete": true, "history": true, "yielded": true}

var codexMonitorPassthroughCommands = map[string]struct{}{
	"app-server":       {},
	"app":              {},
	"apply":            {},
	"archive":          {},
	"cloud":            {},
	"completion":       {},
	"debug":            {},
	"delete":           {},
	"doctor":           {},
	"e":                {},
	"exec":             {},
	"exec-server":      {},
	"features":         {},
	"help":             {},
	"login":            {},
	"logout":           {},
	"mcp":              {},
	"mcp-server":       {},
	"migrate-rollouts": {},
	"plugin":           {},
	"remote-control":   {},
	"review":           {},
	"sandbox":          {},
	"unarchive":        {},
	"update":           {},
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  daemon start          Start the daemon if it is not already running")
	fmt.Fprintln(os.Stderr, "  daemon stop           Stop the running daemon")
	fmt.Fprintln(os.Stderr, "  project add [name]   Register the current project")
	fmt.Fprintln(os.Stderr, "  project list         List registered projects")
	fmt.Fprintln(os.Stderr, "  goal add <content>   Create a goal for the current project")
	fmt.Fprintln(os.Stderr, "  goal list            List goals for the current project")
	fmt.Fprintln(os.Stderr, "  context [-brief]      Print the current goal context for an AI session")
	fmt.Fprintln(os.Stderr, "  pending              Print unanswered human decisions for the current project")
	fmt.Fprintln(os.Stderr, "  watch [--monitor --token string [--once] | -goal string | -project]  Stream monitor actions or diagnostic events")
	fmt.Fprintln(os.Stderr, "  role                 Report the claim-derived role for an agent session")
	fmt.Fprintln(os.Stderr, "  handoff append <handoff-id> <task-id> <kind> <body>  Append a task handoff entry")
	fmt.Fprintln(os.Stderr, "  handoff history <handoff-id> <task-id>  Read task handoff history")
	fmt.Fprintln(os.Stderr, "  handoff complete <handoff-id> <task-id>  Report a handoff complete")
	fmt.Fprintln(os.Stderr, "  handoff goal append <handoff-id> <goal-id> <kind> <body>  Append a goal handoff entry")
	fmt.Fprintln(os.Stderr, "  handoff goal history <handoff-id> <goal-id>  Read goal handoff history")
	fmt.Fprintln(os.Stderr, "  handoff goal complete <handoff-id> <goal-id>  Report a goal handoff complete")
	fmt.Fprintln(os.Stderr, "  handoff yielded <task-id>  Report that the worker yielded")
	fmt.Fprintln(os.Stderr, "  handoff options: --kind, --body, --in-reply-to-id, --after-id, --limit, --report, --capability")
	fmt.Fprintln(os.Stderr, "  stop-check           Emit a Codex continuation when scoped role work remains")
	fmt.Fprintln(os.Stderr, "  monitor-check        Deny an ATCT tool call when the session has no live Monitor")
	fmt.Fprintln(os.Stderr, "  merge-check          Deny merging a goal branch into main before the human approves")
	fmt.Fprintln(os.Stderr, "  session-key          Print the SessionStart key for atct_session_identify")
	fmt.Fprintln(os.Stderr, "  version              Print the installed CLI version")
	fmt.Fprintln(os.Stderr, "  codex shim install [--profile <path>]  Install the transparent Codex shim")
	fmt.Fprintln(os.Stderr, "  codex shim run -- <args>  Run Codex through the installed shim")
	fmt.Fprintln(os.Stderr, "  codex monitor [-- <args>]  Run an interactive Codex session with ATCT monitoring")
	fmt.Fprintln(os.Stderr, "  codex monitor stop     Stop monitors for the current project")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen string   HTTP listen address (default \"127.0.0.1:8787\")")
	fmt.Fprintln(os.Stderr, "  -project string  Select a registered project by name (context, pending)")
	fmt.Fprintln(os.Stderr, "  -project        Filter watch events to what a commander acts on (watch)")
	fmt.Fprintln(os.Stderr, "  -expect string   Expected role for the role command")
	fmt.Fprintln(os.Stderr, "  -agent-session-id string  Session identity for the role command")
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) < 1 {
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	sub := args[0]
	if !validSubcommands[sub] {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", sub)
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	rest := args[1:]
	cfg := cliConfig{subcommand: sub}
	if sub == "daemon" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		action := rest[0]
		if !validDaemonActions[action] {
			fmt.Fprintf(os.Stderr, "unknown daemon action %q\n", action)
			fmt.Fprintln(os.Stderr, "daemon requires an action: start or stop")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.daemonAction = action
		rest = rest[1:]
	}
	if sub == "project" {
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "project requires an action: add or list")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		action := rest[0]
		if !validProjectActions[action] {
			fmt.Fprintf(os.Stderr, "unknown project action %q\n", action)
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.projectAction = action
		rest = rest[1:]
		if action == "add" && len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			cfg.projectName = rest[0]
			rest = rest[1:]
		}
	}
	if sub == "goal" {
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "goal requires an action: add or list")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		action := rest[0]
		if !validGoalActions[action] {
			fmt.Fprintf(os.Stderr, "unknown goal action %q\n", action)
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.goalAction = action
		rest = rest[1:]
		if action == "add" {
			if len(rest) < 1 || strings.HasPrefix(rest[0], "-") {
				fmt.Fprintln(os.Stderr, "goal add requires a title")
				printUsage()
				return cliConfig{}, errInvalidArgs
			}
			cfg.goalTitle = rest[0]
			rest = rest[1:]
		}
	}
	if sub == "handoff" {
		return parseHandoffArgs(cfg, rest)
	}
	if sub == "codex" {
		if len(rest) > 0 && rest[0] == "shim" {
			return parseCodexShimArgs(cfg, rest[1:])
		}
		if len(rest) < 1 || rest[0] != "monitor" {
			fmt.Fprintln(os.Stderr, "codex requires the monitor action")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.codexMonitorAction = "monitor"
		rest = rest[1:]
		if len(rest) > 0 && rest[0] == "stop" {
			if len(rest) != 1 {
				fmt.Fprintln(os.Stderr, "codex monitor stop does not accept Codex arguments; put a literal stop after --")
				printUsage()
				return cliConfig{}, errInvalidArgs
			}
			cfg.codexMonitorAction = "stop"
			return cfg, nil
		}
		monitorArgs := rest
		passthroughArgs := []string(nil)
		hasPassthroughDelimiter := false
		for i, arg := range rest {
			if arg == "--" {
				monitorArgs, passthroughArgs = rest[:i], rest[i+1:]
				hasPassthroughDelimiter = true
				break
			}
		}
		for len(monitorArgs) > 0 {
			name, value, inline := codexMonitorOption(monitorArgs[0])
			if name == "" {
				if codexMonitorRejectedOption(monitorArgs[0]) {
					return cliConfig{}, errInvalidArgs
				}
				break
			}
			if !inline {
				if len(monitorArgs) < 2 || strings.TrimSpace(monitorArgs[1]) == "" || strings.HasPrefix(monitorArgs[1], "-") {
					return cliConfig{}, errInvalidArgs
				}
				value = monitorArgs[1]
				monitorArgs = monitorArgs[2:]
			} else {
				if strings.TrimSpace(value) == "" {
					return cliConfig{}, errInvalidArgs
				}
				monitorArgs = monitorArgs[1:]
			}
			cfg.codexMonitorExplicit = true
			switch name {
			case "--role":
				if cfg.codexMonitorRole != "" {
					return cliConfig{}, errInvalidArgs
				}
				cfg.codexMonitorRole = value
			case "--goal":
				if cfg.codexMonitorGoalID != "" {
					return cliConfig{}, errInvalidArgs
				}
				cfg.codexMonitorGoalID = value
			case "--handoff":
				if cfg.codexMonitorHandoffID != "" {
					return cliConfig{}, errInvalidArgs
				}
				cfg.codexMonitorHandoffID = value
			}
		}
		if cfg.codexMonitorExplicit {
			if len(monitorArgs) > 0 {
				return cliConfig{}, errInvalidArgs
			}
			if err := validateCodexMonitorConfig(cfg); err != nil {
				return cliConfig{}, err
			}
			rest = passthroughArgs
		} else if hasPassthroughDelimiter {
			rest = passthroughArgs
		}
		cfg.codexArgs = append([]string(nil), rest...)
		if len(cfg.codexArgs) > 0 {
			_, cfg.codexMonitorPassthrough = codexMonitorPassthroughCommands[cfg.codexArgs[0]]
		}
		return cfg, nil
	}

	flags := flag.NewFlagSet(sub, flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	contextBrief := false
	contextCheck := false
	if sub == "context" {
		flags.BoolVar(&contextBrief, "brief", false, "print a one-line context summary")
		flags.BoolVar(&contextCheck, "check", false, "exit successfully when context work exists")
	}
	if sub == "context" || sub == "pending" {
		flags.StringVar(&cfg.projectName, "project", "", "select a registered project by name")
	}
	if sub == "role" {
		flags.StringVar(&cfg.roleExpected, "expect", "", "require this role: commander, subcommander, or executor")
		flags.StringVar(&cfg.roleAgentSessionID, "agent-session-id", "", "agent session identity used by session.role")
	}
	if sub == "stop-check" {
		flags.BoolVar(&cfg.stopCheckHookInput, "hook-input", false, "read hook JSON from stdin")
	}
	if sub == "monitor-check" {
		flags.BoolVar(&cfg.monitorCheckHookInput, "hook-input", false, "read hook JSON from stdin")
	}
	if sub == "merge-check" {
		flags.BoolVar(&cfg.mergeCheckHookInput, "hook-input", false, "read hook JSON from stdin")
	}
	if sub == "session-key" {
		flags.BoolVar(&cfg.sessionKeyHookInput, "hook-input", false, "read hook JSON from stdin")
	}
	if sub == "watch" {
		flags.StringVar(&cfg.watchGoalID, "goal", "", "filter watch events to this goal")
		flags.BoolVar(&cfg.watchProjectScope, "project", false, "filter watch events to what a commander acts on")
		flags.BoolVar(&cfg.watchMonitor, "monitor", false, "emit only assignment-bound actions for a Claude Monitor")
		flags.StringVar(&cfg.watchMonitorToken, "token", "", "bind the monitor to its SessionStart token")
		flags.BoolVar(&cfg.watchOnce, "once", false, "exit after delivering the first actionable batch; requires --monitor")
	}
	var description *string
	if sub == "goal" && cfg.goalAction == "add" {
		description = flags.String("d", "", "goal description")
	}
	flags.Parse(rest)
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	cfg.listenAddr = *listenAddr
	watchProjectSpecified := false
	watchGoalSpecified := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			cfg.listenExplicit = true
		}
		if f.Name == "project" {
			if sub != "watch" {
				cfg.projectSpecified = true
			}
			if sub == "watch" {
				watchProjectSpecified = true
			}
		}
		if f.Name == "goal" && sub == "watch" {
			watchGoalSpecified = true
		}
		if f.Name == "expect" {
			cfg.roleExpectedSet = true
		}
	})
	if sub == "watch" && watchProjectSpecified && watchGoalSpecified {
		fmt.Fprintln(os.Stderr, "watch: -goal and -project cannot be used together")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "watch" && cfg.watchMonitor && (watchProjectSpecified || watchGoalSpecified) {
		fmt.Fprintln(os.Stderr, "watch: --monitor does not accept -goal or -project")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "watch" && cfg.watchMonitor && strings.TrimSpace(cfg.watchMonitorToken) == "" {
		fmt.Fprintln(os.Stderr, "watch: --monitor requires --token")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "watch" && cfg.watchOnce && !cfg.watchMonitor {
		fmt.Fprintln(os.Stderr, "watch: --once requires --monitor")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "role" && cfg.roleExpectedSet {
		if err := validateExpectedRole(cfg.roleExpected); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return cliConfig{}, errInvalidArgs
		}
	}
	if sub == "stop-check" && !cfg.stopCheckHookInput {
		fmt.Fprintln(os.Stderr, "stop-check requires --hook-input")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "monitor-check" && !cfg.monitorCheckHookInput {
		fmt.Fprintln(os.Stderr, "monitor-check requires --hook-input")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "merge-check" && !cfg.mergeCheckHookInput {
		fmt.Fprintln(os.Stderr, "merge-check requires --hook-input")
		return cliConfig{}, errInvalidArgs
	}
	if sub == "session-key" && !cfg.sessionKeyHookInput {
		fmt.Fprintln(os.Stderr, "session-key requires --hook-input")
		return cliConfig{}, errInvalidArgs
	}
	if description != nil {
		cfg.goalDescription = *description
	}
	cfg.contextBrief = contextBrief
	cfg.contextCheck = contextCheck
	return cfg, nil
}

func parseHandoffArgs(cfg cliConfig, args []string) (cliConfig, error) {
	cfg.listenAddr = defaultListenAddr
	cfg.handoffScope = "task"

	if len(args) == 0 {
		return invalidHandoffArgs("handoff requires an action: append, history, complete, or yielded")
	}
	if args[0] == "goal" || args[0] == "task" {
		cfg.handoffScope = args[0]
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "entry" {
		args = args[1:]
	}
	if len(args) == 0 {
		return invalidHandoffArgs("handoff %s requires an action", cfg.handoffScope)
	}
	action := args[0]
	if !validHandoffActions[action] {
		return invalidHandoffArgs("unknown handoff action %q", action)
	}
	cfg.handoffAction = action
	args = args[1:]

	positionals, used, err := parseHandoffOptions(&cfg, args)
	if err != nil {
		return invalidHandoffArgs("%v", err)
	}
	if err := validateHandoffOptionUse(action, used); err != nil {
		return invalidHandoffArgs("%v", err)
	}

	switch action {
	case "yielded":
		if cfg.handoffScope != "task" {
			return invalidHandoffArgs("handoff yielded only supports a task ID")
		}
		if len(positionals) != 1 || positionals[0] == "" {
			return invalidHandoffArgs("handoff yielded requires a task ID")
		}
		cfg.handoffTaskID = positionals[0]
	case "append":
		if len(positionals) == 4 && !used.kind && !used.body {
			cfg.handoffEntryKind = positionals[2]
			cfg.handoffEntryBody = positionals[3]
			positionals = positionals[:2]
		} else if len(positionals) != 2 || !used.kind || !used.body {
			return invalidHandoffArgs("handoff %s append requires a handoff ID, %s ID, kind, and body", cfg.handoffScope, cfg.handoffScope)
		}
		if hasEmptyHandoffPositional(positionals) || cfg.handoffEntryKind == "" || cfg.handoffEntryBody == "" {
			return invalidHandoffArgs("handoff %s append requires non-empty identifiers, kind, and body", cfg.handoffScope)
		}
		cfg.handoffID = positionals[0]
		if cfg.handoffScope == "goal" {
			cfg.handoffGoalID = positionals[1]
		} else {
			cfg.handoffTaskID = positionals[1]
		}
	case "history", "complete":
		if len(positionals) != 2 || hasEmptyHandoffPositional(positionals) {
			return invalidHandoffArgs("handoff %s %s requires a handoff ID and %s ID", cfg.handoffScope, action, cfg.handoffScope)
		}
		cfg.handoffID = positionals[0]
		if cfg.handoffScope == "goal" {
			cfg.handoffGoalID = positionals[1]
		} else {
			cfg.handoffTaskID = positionals[1]
		}
	}
	return cfg, nil
}

type handoffOptionUse struct {
	inReplyToID    bool
	kind           bool
	body           bool
	afterID        bool
	limit          bool
	report         bool
	capability     bool
	agentSessionID bool
}

func parseHandoffOptions(cfg *cliConfig, args []string) ([]string, handoffOptionUse, error) {
	var positionals []string
	var used handoffOptionUse
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		name, value, hasValue := splitHandoffOption(arg)
		if !hasValue {
			if i+1 >= len(args) {
				return nil, used, fmt.Errorf("option %s requires a value", name)
			}
			i++
			value = args[i]
		}
		if value == "" && name != "--report" && name != "--complete-report" {
			return nil, used, fmt.Errorf("option %s requires a non-empty value", name)
		}

		switch name {
		case "-listen", "--listen":
			cfg.listenAddr = value
			cfg.listenExplicit = true
		case "--in-reply-to-id":
			inReplyToID, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, used, fmt.Errorf("option --in-reply-to-id must be a positive integer: %w", err)
			}
			if inReplyToID <= 0 {
				return nil, used, errors.New("option --in-reply-to-id must be a positive integer")
			}
			cfg.handoffInReplyToID = inReplyToID
			used.inReplyToID = true
		case "--kind":
			cfg.handoffEntryKind = value
			used.kind = true
		case "--body":
			cfg.handoffEntryBody = value
			used.body = true
		case "--after-id":
			afterID, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, used, fmt.Errorf("option --after-id must be a non-negative integer: %w", err)
			}
			if afterID < 0 {
				return nil, used, errors.New("option --after-id must be a non-negative integer")
			}
			cfg.handoffAfterID = afterID
			used.afterID = true
		case "--limit":
			limit, err := strconv.Atoi(value)
			if err != nil {
				return nil, used, fmt.Errorf("option --limit must be an integer: %w", err)
			}
			cfg.handoffLimit = limit
			used.limit = true
		case "--report", "--complete-report":
			cfg.handoffCompleteReport = value
			used.report = true
		case "--capability", "--caller-capability", "--monitor-capability":
			cfg.handoffCapability = value
			used.capability = true
		case "--agent-session-id":
			cfg.handoffAgentSessionID = value
			used.agentSessionID = true
		default:
			return nil, used, fmt.Errorf("unknown handoff option %q", name)
		}
	}
	return positionals, used, nil
}

func splitHandoffOption(arg string) (name, value string, hasValue bool) {
	if index := strings.IndexByte(arg, '='); index >= 0 {
		return arg[:index], arg[index+1:], true
	}
	return arg, "", false
}

func validateHandoffOptionUse(action string, used handoffOptionUse) error {
	if used.inReplyToID && action != "append" {
		return errors.New("--in-reply-to-id is only valid for handoff append")
	}
	if (used.kind || used.body) && action != "append" {
		return errors.New("--kind and --body are only valid for handoff append")
	}
	if (used.afterID || used.limit) && action != "history" {
		return errors.New("--after-id and --limit are only valid for handoff history")
	}
	if used.report && action != "complete" {
		return errors.New("--report is only valid for handoff complete")
	}
	if (used.capability || used.agentSessionID) && action == "yielded" {
		return errors.New("handoff yielded does not accept caller authorization options")
	}
	return nil
}

func hasEmptyHandoffPositional(positionals []string) bool {
	for _, positional := range positionals {
		if positional == "" {
			return true
		}
	}
	return false
}

func codexMonitorOption(arg string) (name, value string, inline bool) {
	for _, candidate := range []string{"--role", "--goal", "--handoff"} {
		if arg == candidate {
			return candidate, "", false
		}
		prefix := candidate + "="
		if strings.HasPrefix(arg, prefix) {
			return candidate, strings.TrimPrefix(arg, prefix), true
		}
	}
	return "", "", false
}

func codexMonitorRejectedOption(arg string) bool {
	for _, option := range []string{"--scope", "--project", "--task"} {
		if arg == option || strings.HasPrefix(arg, option+"=") {
			return true
		}
	}
	return false
}

func invalidHandoffArgs(format string, args ...any) (cliConfig, error) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	printUsage()
	return cliConfig{}, errInvalidArgs
}

func validateCodexMonitorConfig(cfg cliConfig) error {
	if !cfg.codexMonitorExplicit {
		return nil
	}
	if cfg.codexMonitorRole == "" {
		return errInvalidArgs
	}
	if cfg.codexMonitorGoalID != "" {
		goalID, err := strconv.ParseInt(cfg.codexMonitorGoalID, 10, 64)
		if err != nil || goalID <= 0 {
			return errInvalidArgs
		}
	}
	switch cfg.codexMonitorRole {
	case "commander":
		if cfg.codexMonitorGoalID != "" || cfg.codexMonitorHandoffID != "" {
			return errInvalidArgs
		}
	case "subcommander":
		if cfg.codexMonitorGoalID == "" || cfg.codexMonitorHandoffID == "" {
			return errInvalidArgs
		}
	default:
		return errInvalidArgs
	}
	return nil
}

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func contextExitCode(err error) int {
	if errors.Is(err, store.ErrUnknownSchemaMigration) {
		return 3
	}
	return 1
}

func exitContextError(err error) {
	if code := contextExitCode(err); code != 1 {
		log.Printf("context: %v", err)
		os.Exit(code)
	}
	log.Fatalf("context: %v", err)
}

func prepareDaemonStart(dir string) error {
	reg, err := daemonctl.ReadRegistry(dir)
	if err == nil {
		if reg.Healthy() {
			return fmt.Errorf(
				"daemon is already running: pid %d, http %s; run `atct daemon stop` first",
				reg.PID, reg.HTTPAddr)
		}
	} else if !errors.Is(err, daemonctl.ErrNoRegistry) {
		return err
	}

	if err := os.Remove(daemonctl.SocketPath(dir)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return daemonctl.RemoveRegistry(dir)
}

func main() {
	config, err := parseArgs(os.Args[1:])
	if err != nil {
		os.Exit(2)
	}
	if config.subcommand == "version" {
		fmt.Println(version)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolve home: %v", err)
	}
	dir := filepath.Join(home, ".atct")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", dir, err)
	}

	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve executable: %v", err)
	}
	switch config.subcommand {
	case "daemon":
		switch config.daemonAction {
		case "start":
			reg, err := daemonctl.Ensure(daemonctl.Config{
				Dir:            dir,
				Version:        version,
				Executable:     exePath,
				ListenAddr:     config.listenAddr,
				ListenExplicit: config.listenExplicit,
			})
			if err != nil {
				log.Fatalf("daemon start: %v", err)
			}
			fmt.Fprintf(os.Stderr, "atct daemon ready: pid %d, http %s\n", reg.PID, reg.HTTPAddr)
		case "stop":
			stopped, err := daemonctl.StopWithWatchWarning(daemonctl.Config{Dir: dir, Version: version}, os.Stderr)
			if err != nil {
				log.Fatalf("daemon stop: %v", err)
			}
			if stopped {
				fmt.Fprintln(os.Stderr, "atct daemon stopped")
			} else {
				fmt.Fprintln(os.Stderr, "no atct daemon was running")
			}
		default:
			if err := runDaemon(config, dir); err != nil {
				log.Printf("daemon: %v", err)
				os.Exit(1)
			}
		}
		return
	case "project":
		if err := runProject(config, dir, exePath); err != nil {
			log.Fatalf("project %s: %v", config.projectAction, err)
		}
		return
	case "goal":
		if err := runGoal(config, dir, exePath); err != nil {
			log.Fatalf("goal %s: %v", config.goalAction, err)
		}
		return
	case "handoff":
		if err := runHandoff(config, dir, exePath); err != nil {
			log.Fatalf("handoff %s: %v", config.handoffAction, err)
		}
		return
	case "codex":
		var code int
		switch config.codexShimAction {
		case "install":
			code, err = runCodexShimInstall(config, exePath)
		case "run":
			code, err = runCodexShim(config, dir)
		default:
			code, err = runCodexMonitor(config, dir)
		}
		if err != nil {
			if config.codexShimAction != "" {
				log.Printf("codex shim: %v", err)
			} else {
				log.Printf("codex monitor: %v", err)
			}
			if code == 0 {
				code = 1
			}
		}
		os.Exit(code)
	case "context":
		if config.contextBrief {
			if err := runContextBriefForProject(dir, config.projectName, config.projectSpecified); err != nil {
				exitContextError(err)
			}
			return
		}
		if config.contextCheck {
			if err := runContextCheckForProject(dir, config.projectName, config.projectSpecified); err != nil {
				if errors.Is(err, errNoContextWork) {
					os.Exit(1)
				}
				exitContextError(err)
			}
			return
		}
		if err := runContextForProject(dir, config.projectName, config.projectSpecified); err != nil {
			exitContextError(err)
		}
		return
	case "pending":
		if err := runPendingForProject(dir, config.projectName, config.projectSpecified); err != nil {
			if errors.Is(err, errNoPendingDecisions) {
				os.Exit(1)
			}
			log.Fatalf("pending: %v", err)
		}
		return
	case "role":
		code, err := runRole(config, dir, exePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if code == 0 {
				code = 1
			}
		}
		os.Exit(code)
	case "stop-check":
		if err := runStopCheck(config, dir, exePath); err != nil {
			log.Printf("stop-check: %v", err)
			os.Exit(1)
		}
		return
	case "monitor-check":
		if err := runMonitorCheck(config, dir, exePath); err != nil {
			log.Printf("monitor-check: %v", err)
			os.Exit(1)
		}
		return
	case "merge-check":
		if err := runMergeCheck(dir); err != nil {
			log.Printf("merge-check: %v", err)
			os.Exit(1)
		}
		return
	case "session-key":
		if err := runSessionKey(config, dir); err != nil {
			log.Printf("session-key: %v", err)
			os.Exit(1)
		}
		return
	case "watch":
		if err := runWatchWithOptionsAndToken(dir, config.watchGoalID, config.watchProjectScope, config.watchMonitor, config.watchMonitorToken, config.watchOnce); err != nil {
			log.Fatalf("watch: %v", err)
		}
		return
	}
}

func runDaemon(config cliConfig, dir string) error {
	if err := prepareDaemonStart(dir); err != nil {
		return err
	}

	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sock := filepath.Join(dir, "atct.sock")
	d := daemon.NewWithVersion(s, version, sock)
	httpListener, err := listenHTTP(config.listenAddr, config.listenExplicit)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:    httpListener.Addr().String(),
		Handler: d.HTTPHandler(),
	}
	defer func() {
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			_ = httpServer.Close()
		}
		_ = httpListener.Close()
		// The registry and socket are shared paths. A daemon that outlived a
		// newer one must not delete the newer one's files on the way out:
		// that strands the survivor holding the HTTP port with no socket,
		// and every later start then fails to bind.
		if daemonctl.RegistryOwnedBy(dir, os.Getpid()) {
			_ = daemonctl.RemoveRegistry(dir)
			_ = os.Remove(sock)
		}
	}()

	rpcErr := make(chan error, 1)
	go func() {
		rpcErr <- d.Serve(ctx, sock)
	}()

	httpErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(httpListener)
		if errors.Is(err, http.ErrServerClosed) {
			httpErr <- nil
			return
		}
		httpErr <- err
	}()

	if err := daemonctl.WriteRegistry(dir, daemonRegistry(httpListener, sock, version)); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}

	log.Printf("atct daemon listening on unix socket %s and HTTP %s", sock, httpListener.Addr())
	select {
	case err := <-rpcErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
	case err := <-httpErr:
		if err != nil {
			return fmt.Errorf("http serve: %w", err)
		}
	}
	return nil
}

func listenHTTP(addr string, explicit bool) (net.Listener, error) {
	if explicit || addr != defaultListenAddr {
		listener, err := listenTCP("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("listen HTTP on %s: %w", addr, err)
		}
		return listener, nil
	}

	listener, err := listenTCP("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen HTTP on %s: %w", addr, err)
	}
	return listener, nil
}

func daemonRegistry(listener net.Listener, socketPath, daemonVersion string) daemonctl.Registry {
	return daemonctl.Registry{
		PID:        os.Getpid(),
		HTTPAddr:   listener.Addr().String(),
		SocketPath: socketPath,
		Version:    daemonVersion,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

func runProject(config cliConfig, dir, exePath string) error {
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return err
	}

	client := mcpshim.NewClient(reg.SocketPath)
	ctx := context.Background()
	switch config.projectAction {
	case "add":
		return addProject(ctx, client, config.projectName)
	case "list":
		return listProjects(ctx, client)
	default:
		return fmt.Errorf("unsupported project action %q", config.projectAction)
	}
}

func addProject(ctx context.Context, client *mcpshim.Client, name string) error {
	rootPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	var project domain.Project
	err = client.Call(ctx, "project.create", map[string]string{
		"name":      name,
		"root_path": rootPath,
	}, &project)
	if err == nil {
		fmt.Fprintf(os.Stderr, "registered project %q at %s\n", project.Name, project.RootPath)
		return nil
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed: projects.") {
		return err
	}

	existing, lookupErr := findExistingProject(ctx, client, name, rootPath)
	if lookupErr != nil {
		return fmt.Errorf("project is already registered, but its name could not be determined: %w", lookupErr)
	}
	fmt.Fprintf(os.Stderr, "already registered as %q\n", existing.Name)
	return nil
}

func findExistingProject(ctx context.Context, client *mcpshim.Client, name, rootPath string) (domain.Project, error) {
	var projects []domain.Project
	if err := client.Call(ctx, "project.list", map[string]string{}, &projects); err != nil {
		return domain.Project{}, err
	}
	cleanRoot := filepath.Clean(rootPath)
	if resolved, err := filepath.EvalSymlinks(rootPath); err == nil {
		cleanRoot = filepath.Clean(resolved)
	}
	for _, project := range projects {
		if name != "" && project.Name == name {
			return project, nil
		}
		projectRoot := filepath.Clean(project.RootPath)
		if resolved, err := filepath.EvalSymlinks(project.RootPath); err == nil {
			projectRoot = filepath.Clean(resolved)
		}
		if projectRoot == cleanRoot {
			return project, nil
		}
	}
	return domain.Project{}, fmt.Errorf("no matching project for %s", rootPath)
}

func listProjects(ctx context.Context, client *mcpshim.Client) error {
	var projects []domain.Project
	if err := client.Call(ctx, "project.list", map[string]string{}, &projects); err != nil {
		return err
	}
	for _, project := range projects {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", project.Name, project.RootPath)
	}
	return nil
}

func runGoal(config cliConfig, dir, exePath string) error {
	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return err
	}

	client := mcpshim.NewClient(reg.SocketPath)
	ctx := context.Background()
	switch config.goalAction {
	case "add":
		return addGoal(ctx, client, config.goalTitle, config.goalDescription)
	case "list":
		return listGoals(ctx, client)
	default:
		return fmt.Errorf("unsupported goal action %q", config.goalAction)
	}
}

func runHandoff(config cliConfig, dir, exePath string) error {
	if config.handoffAction == "yielded" {
		reg, err := daemonctl.ReadRegistry(dir)
		if err != nil {
			if errors.Is(err, daemonctl.ErrNoRegistry) {
				return nil
			}
			return err
		}
		if !reg.Healthy() {
			return nil
		}

		client := mcpshim.NewClient(reg.SocketPath)
		return client.Call(context.Background(), "handoff.yielded", map[string]string{
			"task_id": config.handoffTaskID,
		}, nil)
	}

	capability := strings.TrimSpace(config.handoffCapability)
	if capability == "" {
		for _, envName := range []string{
			"ATCT_MONITOR_CAPABILITY",
			"ATCT_MONITORED_CALLER_CAPABILITY",
			"ATCT_CALLER_CAPABILITY",
		} {
			if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
				capability = value
				break
			}
		}
	}
	if capability == "" {
		return daemon.ErrMonitoredCallerRequired
	}
	if config.handoffAction == "complete" && strings.TrimSpace(config.handoffCompleteReport) == "" {
		return errors.New("handoff complete requires a non-empty --report")
	}

	reg, err := daemonctl.Ensure(daemonctl.Config{
		Dir:            dir,
		Version:        version,
		Executable:     exePath,
		ListenAddr:     config.listenAddr,
		ListenExplicit: config.listenExplicit,
	})
	if err != nil {
		return err
	}

	client := mcpshim.NewClient(reg.SocketPath)
	ctx := context.Background()
	params := map[string]any{
		"handoff_id": config.handoffID,
		"capability": capability,
	}
	if config.handoffScope == "goal" {
		params["goal_id"] = config.handoffGoalID
	} else {
		params["task_id"] = config.handoffTaskID
	}
	if config.handoffAgentSessionID != "" {
		agentSessionID, err := strconv.ParseInt(config.handoffAgentSessionID, 10, 64)
		if err != nil || agentSessionID <= 0 {
			return fmt.Errorf("agent session ID must be a positive integer: %q", config.handoffAgentSessionID)
		}
		params["agent_session_id"] = agentSessionID
	}

	switch config.handoffAction {
	case "append":
		params["kind"] = config.handoffEntryKind
		params["body"] = config.handoffEntryBody
		if config.handoffInReplyToID != 0 {
			params["in_reply_to_id"] = config.handoffInReplyToID
		}
		method := "handoff.entry.append"
		var entry cliHandoffEntry
		if config.handoffScope == "goal" {
			method = "goal.handoff.entry.append"
		}
		if err := client.Call(ctx, method, params, &entry); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(entry)

	case "history":
		if config.handoffAfterID != 0 {
			params["after_id"] = config.handoffAfterID
		}
		if config.handoffLimit != 0 {
			params["limit"] = config.handoffLimit
		}
		method := "handoff.entry.history"
		var page cliHandoffEntryPage
		if config.handoffScope == "goal" {
			method = "goal.handoff.entry.history"
		}
		if err := client.Call(ctx, method, params, &page); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(page)

	case "complete":
		params["complete_report"] = config.handoffCompleteReport
		if config.handoffScope == "goal" {
			var handoff store.GoalHandoff
			if err := client.Call(ctx, "goal.handoff.complete", params, &handoff); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "atct handoff: goal %d reported complete\n", handoff.GoalID)
			return nil
		}
		var handoff store.TaskHandoff
		if err := client.Call(ctx, "handoff.complete", params, &handoff); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "atct handoff: task %d reported complete\n", handoff.TaskID)
		return nil
	default:
		return fmt.Errorf("unsupported handoff action %q", config.handoffAction)
	}
}

// addGoal joins the positional argument and --description the same way the
// migration joined a goal's old title and description, so a script that still
// passes the flag keeps producing the content it used to.
func addGoal(ctx context.Context, client *mcpshim.Client, headline, body string) error {
	content := headline
	if strings.TrimSpace(body) != "" {
		content = headline + "\n\n" + body
	}
	rootPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	var goal domain.Goal
	if err := client.Call(ctx, "goal.create", map[string]string{
		"cwd":     rootPath,
		"content": content,
		"creator": "agent",
	}, &goal); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created goal %q\n", domain.Headline(goal.Content))
	return nil
}

func listGoals(ctx context.Context, client *mcpshim.Client) error {
	rootPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	var result struct {
		Goals []domain.Goal `json:"goals"`
	}
	if err := client.Call(ctx, "goal.list", map[string]string{
		"cwd":              rootPath,
		"agent_session_id": "",
	}, &result); err != nil {
		return err
	}
	for _, goal := range result.Goals {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", domain.Headline(goal.Content), goal.Status)
	}
	return nil
}
