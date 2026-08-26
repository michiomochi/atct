package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultListenAddr = "127.0.0.1:8787"

var version = "dev"

func resolveAtctPath(self string) string {
	if configured := os.Getenv("ATCT_ATCT_BIN"); configured != "" {
		if info, err := os.Stat(configured); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return configured
		}
	}

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

	client := mcpshim.NewClient(sock)
	var registerResponse struct {
		AgentSessionID int64 `json:"agent_session_id"`
	}
	if err := client.Call(context.Background(), "run.register", map[string]any{
		"pid": os.Getpid(),
	}, &registerResponse); err != nil {
		log.Fatalf("register agent session: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "atct", Version: version}, &mcp.ServerOptions{
		Instructions: mcpshim.Instructions,
	})
	mcpshim.Register(server, client, registerResponse.AgentSessionID)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
