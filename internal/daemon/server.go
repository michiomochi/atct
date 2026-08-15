package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

type Daemon struct {
	store *store.Store
}

func New(s *store.Store) *Daemon { return &Daemon{store: s} }

func (d *Daemon) Serve(ctx context.Context, socketPath string) error {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", socketPath, err)
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go d.handleConn(ctx, conn)
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		var req rpc.Request
		var resp rpc.Response

		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			resp.Error = fmt.Sprintf("invalid request: %v", err)
		} else {
			result, err := d.dispatch(ctx, req)
			if err != nil {
				resp.Error = err.Error()
			} else {
				resp.Result = result
			}
		}

		out, err := json.Marshal(resp)
		if err != nil {
			return
		}
		if _, err := conn.Write(append(out, '\n')); err != nil {
			return
		}
	}
}
