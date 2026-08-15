package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultListenAddr = "127.0.0.1:8787"

var version = "dev"

func resolveAtctPath(self string) string {
	candidate := filepath.Join(filepath.Dir(self), "atct")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return "atct"
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolve home: %v", err)
	}
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	if _, err := daemonctl.Ensure(daemonctl.Config{
		Dir:        filepath.Join(home, ".atct"),
		Version:    version,
		Executable: resolveAtctPath(self),
		ListenAddr: defaultListenAddr,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "atct-mcp: %v\n", err)
		os.Exit(1)
	}
	sock := filepath.Join(home, ".atct", "atct.sock")

	// run_id is unique per process start and records which execution owns a parked decision.
	runID := uuid.NewString()

	server := mcp.NewServer(&mcp.Implementation{Name: "atct", Version: version}, nil)
	mcpshim.Register(server, mcpshim.NewClient(sock), runID)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
