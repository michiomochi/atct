package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
	atctweb "github.com/michiomochi/atct/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Daemon struct {
	store        *store.Store
	clock        func() time.Time
	version      string
	socketPath   string
	nextPeerID   atomic.Uint64
	capabilityMu sync.Mutex
	capabilities map[string]monitoredCallerCapability
	// stopBlocked is, per agent session, the reason and state the last stop was
	// blocked for. It is memory only: a restarted daemon blocks once more.
	stopMu      sync.Mutex
	stopBlocked map[int64]string
	// toolUse holds the PreToolUse bindings that attach a transport to the
	// session its hook identified. Memory only.
	toolUse toolUseBindings
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

func (d *Daemon) newTracker() *wakeupTracker {
	return newWakeupTracker(d.clock())
}

// HTTPHandler returns the daemon's HTTP handler, including the API and the
// embedded Web UI. API routes are registered before the UI fallback so an API
// typo cannot be answered with the index document.
func (d *Daemon) HTTPHandler() http.Handler {
	api := httpapi.New(d.store).Handler()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		// Claude Code sends server/discover on the same connection before
		// initialize. Registering for it leaves an agent_sessions row no client
		// uses; refusing it lets the client go on to initialize, which registers once.
		if r.Header.Get("Mcp-Method") == "server/discover" {
			return nil
		}
		client := mcpshim.NewClient(d.socketPath)
		var registerResponse struct {
			AgentSessionID int64 `json:"agent_session_id"`
		}
		if err := retryWhileBusy(func() error {
			return client.Call(r.Context(), "run.register", map[string]any{
				"pid": os.Getpid(),
			}, &registerResponse)
		}); err != nil {
			// The SDK answers nil with a bare 400 "no server available";
			// mcpRegisterFailureWriter turns it into a 503 naming err, and this log keeps it.
			log.Printf("mcp: register agent session: %v", err)
			if f, ok := r.Context().Value(mcpRegisterFailureKey{}).(*mcpRegisterFailure); ok {
				f.err = err
			}
			return nil
		}
		// One client opens several transports and agent_sessions has no column
		// saying which made a row; this line is the only trace.
		log.Printf("mcp: registered agent session %d (remote %s, user-agent %q)", registerResponse.AgentSessionID, r.RemoteAddr, r.UserAgent())

		server := mcp.NewServer(&mcp.Implementation{Name: "atct", Version: d.version}, &mcp.ServerOptions{
			Instructions: mcpshim.Instructions,
		})
		mcpshim.Register(server, client, registerResponse.AgentSessionID)
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
			failure := &mcpRegisterFailure{}
			r = r.WithContext(context.WithValue(r.Context(), mcpRegisterFailureKey{}, failure))
			mcpHandler.ServeHTTP(&mcpRegisterFailureWriter{ResponseWriter: w, failure: failure}, r)
			return
		}

		if serveEmbeddedFile(w, r, dist, static) {
			return
		}
		if looksLikeEmbeddedAsset(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		locale, err := d.store.GetUILocale(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("resolve UI locale: %v", err), http.StatusInternalServerError)
			return
		}
		serveEmbeddedWeb(w, r, dist, locale)
	})
}

// registerAttempts is how many times run.register is tried while the database
// is busy. Each try already waits busy_timeout (5s) for the lock.
const registerAttempts = 3

func retryWhileBusy(call func() error) (err error) {
	for i := 0; i < registerAttempts; i++ {
		if err = call(); err == nil || !strings.Contains(err.Error(), "SQLITE_BUSY") {
			return err
		}
	}
	return err
}

type mcpRegisterFailureKey struct{}

// mcpRegisterFailure carries a run.register failure from the NewStreamableHTTPHandler
// callback, which can only return nil, to the response writer.
type mcpRegisterFailure struct{ err error }

// mcpRegisterFailureWriter answers a failed registration with 503 + Retry-After and
// the cause, instead of the SDK's 400 "no server available", which is also what a
// refused server/discover gets. The client can tell the two apart and retry.
type mcpRegisterFailureWriter struct {
	http.ResponseWriter
	failure *mcpRegisterFailure
	swapped bool
}

func (w *mcpRegisterFailureWriter) WriteHeader(code int) {
	if w.failure.err != nil && code == http.StatusBadRequest {
		w.swapped = true
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w.ResponseWriter, "atct daemon could not register an agent session (retry): %v\n", w.failure.err)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *mcpRegisterFailureWriter) Write(p []byte) (int, error) {
	if w.swapped {
		return len(p), nil // the SDK's own body
	}
	return w.ResponseWriter.Write(p)
}

func (w *mcpRegisterFailureWriter) Flush() { http.NewResponseController(w.ResponseWriter).Flush() }

func (w *mcpRegisterFailureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func serveEmbeddedFile(w http.ResponseWriter, r *http.Request, dist fs.FS, static http.Handler) bool {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name != "" && name != "." && fs.ValidPath(name) && embeddedFileExists(dist, name) {
		static.ServeHTTP(w, r)
		return true
	}
	return false
}

func serveEmbeddedWeb(w http.ResponseWriter, r *http.Request, dist fs.FS, locale string) {
	localeDist, err := fs.Sub(dist, locale)
	if err != nil {
		http.Error(w, fmt.Sprintf("web locale %q: %v", locale, err), http.StatusInternalServerError)
		return
	}
	static := http.FileServer(http.FS(localeDist))

	indexRequest := r.Clone(r.Context())
	indexRequest.URL.Path = "/"
	indexRequest.URL.RawPath = ""
	requestPath := stripEmbeddedLocalePrefix(r.URL.Path)
	if dynamicIndexPath := embeddedDynamicIndexPath(localeDist, requestPath); dynamicIndexPath != "" {
		indexRequest.URL.Path = dynamicIndexPath
	}
	static.ServeHTTP(w, indexRequest)
}

func stripEmbeddedLocalePrefix(requestPath string) string {
	cleanPath := path.Clean(requestPath)
	if cleanPath == "." || cleanPath == "/" {
		return "/"
	}
	segment := strings.TrimPrefix(cleanPath, "/")
	if slash := strings.IndexByte(segment, '/'); slash >= 0 {
		segment = segment[:slash]
	}
	if segment != "en" && segment != "ja" {
		return cleanPath
	}
	remainder := strings.TrimPrefix(cleanPath, "/"+segment)
	if remainder == "" {
		return "/"
	}
	return remainder
}

func looksLikeEmbeddedAsset(requestPath string) bool {
	cleanPath := path.Clean(requestPath)
	if cleanPath == "." || cleanPath == "/" {
		return false
	}
	segments := strings.Split(strings.Trim(cleanPath, "/"), "/")
	for _, segment := range segments {
		if segment == "_astro" {
			return true
		}
	}
	return path.Ext(cleanPath) != ""
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
	tracker := d.newTracker()
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
		peerID := d.nextPeerID.Add(1)
		go d.handleConn(ctx, conn, peerID)
	}
}

func (d *Daemon) handleConn(ctx context.Context, conn net.Conn, peerID uint64) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		var req rpc.Request
		var resp rpc.Response

		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			resp.Error = fmt.Sprintf("invalid request: %v", err)
		} else {
			result, err := d.dispatchWithPeer(ctx, req, peerID)
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
