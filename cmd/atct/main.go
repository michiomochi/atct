package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	subcommand         string
	daemonAction       string
	listenAddr         string
	listenExplicit     bool
	contextBrief       bool
	contextCheck       bool
	handoffAction      string
	handoffID          string
	handoffTaskID      string
	projectSpecified   bool
	projectAction      string
	projectName        string
	goalAction         string
	goalTitle          string
	goalDescription    string
	taskIDs            []string
	roleExpected       string
	roleExpectedSet    bool
	roleAgentSessionID string
	watchGoalID        string
	watchProjectScope  bool
}

var errInvalidArgs = errors.New("invalid command line")

var validSubcommands = map[string]bool{
	"daemon":      true,
	"project":     true,
	"goal":        true,
	"context":     true,
	"pending":     true,
	"watch":       true,
	"claim-check": true,
	"role":        true,
	"handoff":     true,
}

var validDaemonActions = map[string]bool{"start": true, "stop": true}
var validProjectActions = map[string]bool{"add": true, "list": true}
var validGoalActions = map[string]bool{"add": true, "list": true}
var validHandoffActions = map[string]bool{"complete": true, "yielded": true}

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
	fmt.Fprintln(os.Stderr, "  watch [-goal string] [-project]  Stream human decision events for a Monitor")
	fmt.Fprintln(os.Stderr, "  claim-check <ids...>|any  Exit 0 only if the tasks are claimed by a running session")
	fmt.Fprintln(os.Stderr, "  role                 Report the claim-derived role for an agent session")
	fmt.Fprintln(os.Stderr, "  handoff complete <handoff-id> <task-id>  Report a handoff complete")
	fmt.Fprintln(os.Stderr, "  handoff yielded <task-id>  Report that the worker yielded")
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
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "handoff requires an action: complete")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		action := rest[0]
		if !validHandoffActions[action] {
			fmt.Fprintf(os.Stderr, "unknown handoff action %q\n", action)
			fmt.Fprintln(os.Stderr, "handoff requires an action: complete")
			printUsage()
			return cliConfig{}, errInvalidArgs
		}
		cfg.handoffAction = action
		rest = rest[1:]
		if action == "yielded" {
			if len(rest) < 1 || strings.HasPrefix(rest[0], "-") {
				fmt.Fprintln(os.Stderr, "handoff yielded requires a task ID")
				printUsage()
				return cliConfig{}, errInvalidArgs
			}
			cfg.handoffTaskID = rest[0]
			rest = rest[1:]
		} else {
			if len(rest) < 2 || strings.HasPrefix(rest[0], "-") || strings.HasPrefix(rest[1], "-") {
				fmt.Fprintln(os.Stderr, "handoff complete requires a handoff ID and task ID")
				printUsage()
				return cliConfig{}, errInvalidArgs
			}
			cfg.handoffID = rest[0]
			cfg.handoffTaskID = rest[1]
			rest = rest[2:]
		}
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
	if sub == "watch" {
		flags.StringVar(&cfg.watchGoalID, "goal", "", "filter watch events to this goal")
		flags.BoolVar(&cfg.watchProjectScope, "project", false, "filter watch events to what a commander acts on")
	}
	var description *string
	if sub == "goal" && cfg.goalAction == "add" {
		description = flags.String("d", "", "goal description")
	}
	flags.Parse(rest)
	if sub == "claim-check" {
		cfg.taskIDs = flags.Args()
	} else if len(flags.Args()) > 0 {
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
	if sub == "role" && cfg.roleExpectedSet {
		if err := validateExpectedRole(cfg.roleExpected); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return cliConfig{}, errInvalidArgs
		}
	}
	if description != nil {
		cfg.goalDescription = *description
	}
	cfg.contextBrief = contextBrief
	cfg.contextCheck = contextCheck
	return cfg, nil
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
	case "claim-check":
		code, err := claimCheckCommand(dir, config.taskIDs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	case "role":
		code, err := runRole(config, dir, exePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			if code == 0 {
				code = 1
			}
		}
		os.Exit(code)
	case "watch":
		if err := runWatch(dir, config.watchGoalID); err != nil {
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
		_ = daemonctl.RemoveRegistry(dir)
		_ = os.Remove(sock)
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
	var handoff store.TaskHandoff
	if err := client.Call(context.Background(), "handoff.complete", map[string]string{
		"handoff_id": config.handoffID,
		"task_id":    config.handoffTaskID,
	}, &handoff); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "atct handoff: task %d reported complete\n", handoff.TaskID)
	return nil
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
