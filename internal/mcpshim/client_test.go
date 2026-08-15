package mcpshim

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/daemon"
	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestClientCallReachesDaemon(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock := filepath.Join(dir, "atct.sock")
	go daemon.New(s).Serve(ctx, sock)

	for i := 0; i < 50; i++ {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	client := NewClient(sock)
	var ns domain.Project
	err = client.Call(ctx, "project.create",
		map[string]string{"name": "atct", "root_path": "/repos/atct"}, &ns)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if ns.Name != "atct" {
		t.Fatalf("name = %q, want %q", ns.Name, "atct")
	}
}
