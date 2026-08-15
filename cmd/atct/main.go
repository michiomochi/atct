package main

import (
	"context"
	"errors"
	"flag"
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

func main() {
	listenAddr := flag.String("listen", defaultListenAddr, "HTTP listen address")
	flag.Parse()

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
	httpServer := &http.Server{Addr: *listenAddr, Handler: d.HTTPHandler()}

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

	log.Printf("atct daemon listening on unix socket %s and HTTP %s", sock, *listenAddr)
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
