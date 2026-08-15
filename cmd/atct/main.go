package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/store"
)

const defaultListenAddr = "127.0.0.1:8787"

type cliConfig struct {
	subcommand string
	listenAddr string
}

var errInvalidArgs = errors.New("invalid command line")

var validSubcommands = map[string]bool{"daemon": true, "ensure": true, "stop": true}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct <command> [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  daemon    Run the ATCT daemon in the foreground")
	fmt.Fprintln(os.Stderr, "  ensure    Start the daemon if it is not already running")
	fmt.Fprintln(os.Stderr, "  stop      Stop the running daemon")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen string   HTTP listen address (default \"127.0.0.1:8787\")")
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

	flags := flag.NewFlagSet(sub, flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	flags.Parse(args[1:])
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	return cliConfig{subcommand: sub, listenAddr: *listenAddr}, nil
}

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

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
			Dir: dir, Version: version, Executable: exePath, ListenAddr: config.listenAddr,
		})
		if err != nil {
			log.Fatalf("ensure: %v", err)
		}
		fmt.Fprintf(os.Stderr, "atct daemon ready: pid %d, http %s\n", reg.PID, reg.HTTPAddr)
		return
	case "stop":
		stopped, err := daemonctl.Stop(daemonctl.Config{Dir: dir, Version: version})
		if err != nil {
			log.Fatalf("stop: %v", err)
		}
		if stopped {
			fmt.Fprintln(os.Stderr, "atct daemon stopped")
		} else {
			fmt.Fprintln(os.Stderr, "no atct daemon was running")
		}
		return
	}

	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer s.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sock := filepath.Join(dir, "atct.sock")
	d := daemon.New(s)
	httpServer := &http.Server{Addr: config.listenAddr, Handler: d.HTTPHandler()}

	rpcErr := make(chan error, 1)
	go func() {
		rpcErr <- d.Serve(ctx, sock)
	}()

	httpErr := make(chan error, 1)
	go func() {
		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			httpErr <- nil
			return
		}
		httpErr <- err
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		_ = daemonctl.RemoveRegistry(dir)
		_ = os.Remove(sock)
	}()

	if err := daemonctl.WriteRegistry(dir, daemonctl.Registry{
		PID:        os.Getpid(),
		HTTPAddr:   config.listenAddr,
		SocketPath: sock,
		Version:    version,
		StartedAt:  time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		log.Fatalf("write registry: %v", err)
	}

	log.Printf("atct daemon listening on unix socket %s and HTTP %s", sock, config.listenAddr)
	select {
	case err := <-rpcErr:
		if err != nil {
			log.Fatalf("serve: %v", err)
		}
	case err := <-httpErr:
		if err != nil {
			log.Fatalf("http serve: %v", err)
		}
	}
}
