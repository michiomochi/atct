package httpapi_test

import (
	"sync"

	"github.com/michiomochi/atct/internal/store"
	"testing"
)

var httpTestSessionIDs = struct {
	sync.Mutex
	ids  map[string]int64
	next int64
}{ids: make(map[string]int64), next: 100000}

func testSessionID(label string) int64 {
	httpTestSessionIDs.Lock()
	defer httpTestSessionIDs.Unlock()
	if id, ok := httpTestSessionIDs.ids[label]; ok {
		return id
	}
	httpTestSessionIDs.next++
	httpTestSessionIDs.ids[label] = httpTestSessionIDs.next
	return httpTestSessionIDs.next
}

func registerTestSession(t *testing.T, s *store.Store, label string, pid int) int64 {
	t.Helper()
	id := testSessionID(label)
	if _, err := s.DB().ExecContext(t.Context(), `
		INSERT OR IGNORE INTO agent_sessions (id, pid, started_at, registered_at)
		VALUES (?, ?, '', datetime('now'))
	`, id, pid); err != nil {
		t.Fatalf("register test session %q: %v", label, err)
	}
	return id
}
