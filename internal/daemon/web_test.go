package daemon

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
	atctweb "github.com/michiomochi/atct/web"
)

func TestHTTPHandlerServesEmbeddedIndex(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q, want text/html", response.Header().Get("Content-Type"))
	}
	if !strings.Contains(strings.ToLower(response.Body.String()), "<html") {
		t.Fatal("response does not contain embedded HTML")
	}
}

func TestHTTPHandlerRoutesAPI(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if _, ok := payload["open_decisions"]; !ok {
		t.Fatal("API response is missing open_decisions")
	}
}

func TestHTTPHandlerKeepsSSEOpenUntilDisconnect(t *testing.T) {
	d := newWebTestDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	response := &notifyingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		wrote:            make(chan struct{}),
	}
	request := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		d.HTTPHandler().ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-response.wrote:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not write its initial response")
	}
	select {
	case <-done:
		t.Fatal("SSE handler closed before the client disconnected")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not close after the client disconnected")
	}
}

func TestHTTPHandlerRoutesEmbeddedDynamicPagesAndFallsBackToRoot(t *testing.T) {
	d := newWebTestDaemon(t)

	wantRoot, err := fs.ReadFile(atctweb.Dist, "dist/index.html")
	if err != nil {
		t.Fatalf("read root index: %v", err)
	}
	wantGoal, err := fs.ReadFile(atctweb.Dist, "dist/goals/_/index.html")
	if err != nil {
		t.Fatalf("read goal history template: %v", err)
	}
	wantTask, err := fs.ReadFile(atctweb.Dist, "dist/tasks/_/index.html")
	if err != nil {
		t.Fatalf("read task history template: %v", err)
	}

	tests := []struct {
		name string
		path string
		want []byte
	}{
		{name: "root", path: "/", want: wantRoot},
		{name: "goal detail", path: "/goals/example", want: wantGoal},
		{name: "task detail", path: "/tasks/example", want: wantTask},
		{name: "unknown path", path: "/nonexistent/xxx", want: wantRoot},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if response.Body.String() != string(tt.want) {
				t.Fatalf("route %s returned unexpected embedded page", tt.path)
			}
		})
	}
}

func TestHTTPHandlerReturnsJSON404ForUnknownAPI(t *testing.T) {
	d := newWebTestDaemon(t)

	response := httptest.NewRecorder()
	d.HTTPHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/unknown", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if payload["error"] == "" {
		t.Fatal("JSON 404 is missing error")
	}
}

func newWebTestDaemon(t *testing.T) *Daemon {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return New(s)
}

type notifyingResponseWriter struct {
	*httptest.ResponseRecorder
	wrote chan struct{}
	once  sync.Once
}

func (w *notifyingResponseWriter) notify() {
	w.once.Do(func() { close(w.wrote) })
}

func (w *notifyingResponseWriter) WriteHeader(code int) {
	w.notify()
	w.ResponseRecorder.WriteHeader(code)
}

func (w *notifyingResponseWriter) Write(body []byte) (int, error) {
	w.notify()
	return w.ResponseRecorder.Write(body)
}

func (w *notifyingResponseWriter) Flush() {
	w.notify()
	w.ResponseRecorder.Flush()
}
