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
	"time"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
	atctweb "github.com/michiomochi/atct/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Daemon struct {
	store      *store.Store
	clock      func() time.Time
	version    string
	socketPath string
}

const defaultVersion = "dev"

func New(s *store.Store) *Daemon {
	return newDaemonWithClockAndVersion(s, time.Now, defaultVersion, "")
}

func NewWithVersion(s *store.Store, version, socketPath string) *Daemon {
	return newDaemonWithClockAndVersion(s, time.Now, version, socketPath)
}

func newDaemonWithClock(s *store.Store, clock func() time.Time) *Daemon {
	return newDaemonWithClockAndVersion(s, clock, defaultVersion, "")
}

func newDaemonWithClockAndVersion(s *store.Store, clock func() time.Time, version, socketPath string) *Daemon {
	if clock == nil {
		clock = time.Now
	}
	return &Daemon{store: s, clock: clock, version: version, socketPath: socketPath}
}

// HTTPHandler returns the daemon's HTTP handler, including the API and the
// embedded Web UI. API routes are registered before the UI fallback so an API
// typo cannot be answered with the index document.
func (d *Daemon) HTTPHandler() http.Handler {
	api := httpapi.New(d.store).Handler()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		agentSessionID := uuid.NewString()
		client := mcpshim.NewClient(d.socketPath)
		if err := client.Call(r.Context(), "run.register", map[string]any{
			"agent_session_id": agentSessionID,
			"pid":              os.Getpid(),
		}, nil); err != nil {
			return nil
		}

		server := mcp.NewServer(&mcp.Implementation{Name: "atct", Version: d.version}, &mcp.ServerOptions{
			Instructions: mcpshim.Instructions,
		})
		mcpshim.Register(server, client, agentSessionID)
		return server
	}, nil)
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
		if r.URL.Path == "/mcp" {
			mcpHandler.ServeHTTP(w, r)
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
	if dynamicIndexPath := embeddedDynamicIndexPath(dist, r.URL.Path); dynamicIndexPath != "" {
		indexRequest.URL.Path = dynamicIndexPath
	}
	static.ServeHTTP(w, indexRequest)
}

func embeddedDynamicIndexPath(dist fs.FS, requestPath string) string {
	cleanPath := path.Clean(requestPath)
	if cleanPath == "." || cleanPath == "/" {
		return ""
	}

	segment := strings.TrimPrefix(cleanPath, "/")
	if slash := strings.IndexByte(segment, '/'); slash >= 0 {
		segment = segment[:slash]
	}
	if segment == "" {
		return ""
	}

	indexPath := path.Join(segment, "_", "index.html")
	if !fs.ValidPath(indexPath) || !embeddedFileExists(dist, indexPath) {
		return ""
	}
	return "/" + segment + "/_/"
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

	tickerCtx, stopTicker := context.WithCancel(ctx)
	tickerDone := make(chan struct{})
	tracker := newWakeupTracker()
	go func() {
		defer close(tickerDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-tickerCtx.Done():
				return
			case <-ticker.C:
				d.runMaintenance(tickerCtx, tracker, d.clock())
			}
		}
	}()
	defer func() {
		stopTicker()
		<-tickerDone
	}()

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
