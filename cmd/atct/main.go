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
	"github.com/michiomochi/atct/internal/store"
)

const defaultListenAddr = "127.0.0.1:8787"

type cliConfig struct {
	listenAddr string
}

var errInvalidArgs = errors.New("invalid command line")

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: atct daemon [options]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Options:")
	fmt.Fprintln(os.Stderr, "  -listen address")
}

func parseArgs(args []string) (cliConfig, error) {
	if len(args) < 1 {
		printUsage()
		return cliConfig{}, errInvalidArgs
	}
	if args[0] != "daemon" {
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	flags := flag.NewFlagSet("daemon", flag.ExitOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = printUsage
	listenAddr := flags.String("listen", defaultListenAddr, "HTTP listen address")
	flags.Parse(args[1:])
	if len(flags.Args()) > 0 {
		fmt.Fprintf(os.Stderr, "unexpected argument %q\n", flags.Args()[0])
		printUsage()
		return cliConfig{}, errInvalidArgs
	}

	return cliConfig{listenAddr: *listenAddr}, nil
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
	}()

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
