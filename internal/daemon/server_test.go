package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestDaemonRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sock := filepath.Join(dir, "atct.sock")
	d := New(s)
	go d.Serve(ctx, sock)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	params, _ := json.Marshal(map[string]string{"name": "atct", "root_path": "/repos/atct"})
	req, _ := json.Marshal(rpc.Request{Method: "project.create", Params: params})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("rpc error: %s", resp.Error)
	}
	if len(resp.Result) == 0 {
		t.Fatal("empty result")
	}
}

func newDaemonConn(t *testing.T) net.Conn {
	t.Helper()
	dir, err := os.MkdirTemp("", "atct")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sock := filepath.Join(dir, "atct.sock")
	go New(s).Serve(ctx, sock)

	var conn net.Conn
	for i := 0; i < 50; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func call(t *testing.T, conn net.Conn, method string, params any) rpc.Response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %s params: %v", method, err)
	}
	req, err := json.Marshal(rpc.Request{Method: method, Params: raw})
	if err != nil {
		t.Fatalf("marshal %s request: %v", method, err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		t.Fatalf("write %s: %v", method, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read %s: %v", method, err)
	}
	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("unmarshal %s: %v", method, err)
	}
	return resp
}

func TestDaemonListsProjects(t *testing.T) {
	conn := newDaemonConn(t)
	created := call(t, conn, "project.create", map[string]string{"name": "atct", "root_path": "/repos/atct"})
	if created.Error != "" {
		t.Fatalf("project.create: %s", created.Error)
	}

	listed := call(t, conn, "project.list", map[string]string{})
	if listed.Error != "" {
		t.Fatalf("project.list: %s", listed.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(listed.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project.list returned %d projects, want 1", len(projects))
	}
	if projects[0].Name != "atct" {
		t.Fatalf("name = %q, want %q", projects[0].Name, "atct")
	}
}

func TestDaemonListsProjectsWhenNoneExist(t *testing.T) {
	resp := call(t, newDaemonConn(t), "project.list", map[string]string{})
	if resp.Error != "" {
		t.Fatalf("project.list on an empty store: %s", resp.Error)
	}
	var projects []domain.Project
	if err := json.Unmarshal(resp.Result, &projects); err != nil {
		t.Fatalf("unmarshal projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("got %d projects, want 0", len(projects))
	}
}
