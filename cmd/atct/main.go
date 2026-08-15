package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/store"
)

func main() {
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
	log.Printf("atct daemon listening on %s", sock)
	if err := daemon.New(s).Serve(ctx, sock); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
