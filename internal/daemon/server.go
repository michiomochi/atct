package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
	atctweb "github.com/michiomochi/atct/web"
)

type Daemon struct {
	store *store.Store
}

func New(s *store.Store) *Daemon { return &Daemon{store: s} }

// HTTPHandler returns the daemon's HTTP handler, including the API and the
// embedded Web UI. API routes are registered before the UI fallback so an API
// typo cannot be answered with the index document.
func (d *Daemon) HTTPHandler() http.Handler {
	api := httpapi.New(d.store).Handler()
	dist, err := fs.Sub(atctweb.Dist, "dist")
	if err != nil {
		panic(fmt.Sprintf("web assets: %v", err))
	}
	static := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}

		serveEmbeddedWeb(w, r, dist, static)
	})
}

func serveEmbeddedWeb(w http.ResponseWriter, r *http.Request, dist fs.FS, static http.Handler) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "." && fs.ValidPath(name) && embeddedFileExists(dist, name) {
		static.ServeHTTP(w, r)
		return
	}

	indexRequest := r.Clone(r.Context())
	indexRequest.URL.Path = "/"
	indexRequest.URL.RawPath = ""
	if strings.HasPrefix(r.URL.Path, "/goals/") {
		indexRequest.URL.Path = "/goals/_/"
	}
	static.ServeHTTP(w, indexRequest)
}

func embeddedFileExists(dist fs.FS, name string) bool {
	file, err := dist.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()

	info, err := file.Stat()
	return err == nil && !info.IsDir()
}

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
