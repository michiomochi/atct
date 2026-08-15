package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolve home: %v", err)
	}
	sock := filepath.Join(home, ".atct", "atct.sock")

	// run_id is unique per process start and records which execution owns a parked decision.
	runID := uuid.NewString()

	server := mcp.NewServer(&mcp.Implementation{Name: "atct", Version: "v0.1.0"}, nil)
	mcpshim.Register(server, mcpshim.NewClient(sock), runID)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
