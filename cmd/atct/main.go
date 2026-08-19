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
	lastListenPort    = 8796
)

type cliConfig struct {
	subcommand       string
	listenAddr       string
	listenExplicit   bool
	contextCheck     bool
	projectSpecified bool
	projectAction    string
	projectName      string
	goalAction       string
	goalTitle        string
	goalDescription  string
}

var errInvalidArgs = errors.New("invalid command line")

var validSubcommands = map[string]bool{
	"daemon":  true,
	"ensure":  true,
	"stop":    true,
	"project": true,
	"goal":    true,
	"context": true,
	"pending": true,
	"watch":   true,
}

var validProjectActions = map[string]bool{"add": true, "list": true}
var validGoalActions = map[string]bool{"add": true, "list": true}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  daemon    Run the ATCT daemon in the foreground")
	fmt.Fprintln(os.Stderr, "  ensure    Start the daemon if it is not already running")
	fmt.Fprintln(os.Stderr, "  stop      Stop the running daemon")
	fmt.Fprintln(os.Stderr, "  project add [name]   Register the current project")
	fmt.Fprintln(os.Stderr, "  project list         List registered projects")
	fmt.Fprintln(os.Stderr, "  goal add <title>     Create a goal for the current project")
	fmt.Fprintln(os.Stderr, "  goal list            List goals for the current project")
	fmt.Fprintln(os.Stderr, "  context              Print the current goal context for an AI session")
	fmt.Fprintln(os.Stderr, "  pending              Print unanswered human decisions for the current project")
	fmt.Fprintln(os.Stderr, "  watch                Stream human decision events for a Monitor")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen string   HTTP listen address (default \"127.0.0.1:8787\")")
	fmt.Fprintln(os.Stderr, "  -project string  Select a registered project by name (context, pending)")
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

	flags := flag.NewFlagSet(sub, flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	contextCheck := false
	if sub == "context" {
		flags.BoolVar(&contextCheck, "check", false, "exit successfully when context work exists")
	}
	if sub == "context" || sub == "pending" {
		flags.StringVar(&cfg.projectName, "project", "", "select a registered project by name")
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
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			cfg.listenExplicit = true
		}
		if f.Name == "project" {
			cfg.projectSpecified = true
		}
	})
	if description != nil {
		cfg.goalDescription = *description
	}
	cfg.contextCheck = contextCheck
	return cfg, nil
}

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func prepareDaemonStart(dir string) error {
	reg, err := daemonctl.ReadRegistry(dir)
	if err == nil {
		if reg.Healthy() {
			return fmt.Errorf(
				"daemon is already running: pid %d, http %s; run `atct stop` first",
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
	case "ensure":
		reg, err := daemonctl.Ensure(daemonctl.Config{
			Dir:            dir,
			Version:        version,
			Executable:     exePath,
			ListenAddr:     config.listenAddr,
			ListenExplicit: config.listenExplicit,
		})
		if err != nil {
			log.Fatalf("ensure: %v", err)
		}
		fmt.Fprintf(os.Stderr, "atct daemon ready: pid %d, http %s\n", reg.PID, reg.HTTPAddr)
		return
	case "stop":
		stopped, err := daemonctl.StopWithWatchWarning(daemonctl.Config{Dir: dir, Version: version}, os.Stderr)
		if err != nil {
			log.Fatalf("stop: %v", err)
		}
		if stopped {
			fmt.Fprintln(os.Stderr, "atct daemon stopped")
		} else {
			fmt.Fprintln(os.Stderr, "no atct daemon was running")
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
	case "context":
		if config.contextCheck {
			if err := runContextCheckForProject(dir, config.projectName, config.projectSpecified); err != nil {
				if errors.Is(err, errNoContextWork) {
					os.Exit(1)
				}
				log.Fatalf("context: %v", err)
			}
			return
		}
		if err := runContextForProject(dir, config.projectName, config.projectSpecified); err != nil {
			log.Fatalf("context: %v", err)
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
	case "watch":
		if err := runWatch(dir); err != nil {
			log.Fatalf("watch: %v", err)
		}
		return
	}
	if err := runDaemon(config, dir); err != nil {
		log.Printf("daemon: %v", err)
		os.Exit(1)
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
	d := daemon.New(s)
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
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("listen HTTP on %s: %w", addr, err)
		}
		return listener, nil
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("parse default HTTP listen address %s: %w", addr, err)
	}
	var lastErr error
	for port := defaultListenPort; port <= lastListenPort; port++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(port))
		listener, err := net.Listen("tcp", candidate)
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("listen HTTP on %s: tried ports %d-%d: %w", host, defaultListenPort, lastListenPort, lastErr)
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

func addGoal(ctx context.Context, client *mcpshim.Client, title, description string) error {
	rootPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	var goal domain.Goal
	if err := client.Call(ctx, "goal.create", map[string]string{
		"cwd":         rootPath,
		"title":       title,
		"description": description,
	}, &goal); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created goal %q\n", goal.Title)
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
		"cwd":    rootPath,
		"run_id": "",
	}, &result); err != nil {
		return err
	}
	for _, goal := range result.Goals {
		fmt.Fprintf(os.Stdout, "%s\t%s\n", goal.Title, goal.Status)
	}
	return nil
}
