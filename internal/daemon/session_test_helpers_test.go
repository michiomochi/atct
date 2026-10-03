package daemon

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

var daemonTestSessionRegistry = struct {
	sync.Mutex
	ids  map[*store.Store]map[string]int64
	next int64
}{ids: make(map[*store.Store]map[string]int64), next: 200000}

func daemonTestSessionID(t *testing.T, s *store.Store, label string) int64 {
	t.Helper()
	return daemonTestSessionIDWithPID(t, s, label, os.Getpid())
}

func daemonTestSessionIDWithPID(t *testing.T, s *store.Store, label string, pid int) int64 {
	t.Helper()
	daemonTestSessionRegistry.Lock()
	byStore := daemonTestSessionRegistry.ids[s]
	if byStore == nil {
		byStore = make(map[string]int64)
		daemonTestSessionRegistry.ids[s] = byStore
	}
	if id, ok := byStore[label]; ok {
		daemonTestSessionRegistry.Unlock()
		return id
	}
	daemonTestSessionRegistry.next++
	id := daemonTestSessionRegistry.next
	daemonTestSessionRegistry.Unlock()

	rawID, err := s.RegisterAgentSession(context.Background(), pid)
	if err != nil {
		t.Fatalf("register daemon test session %q: %v", label, err)
	}
	if rawID != id {
		if _, err := s.DB().ExecContext(context.Background(), `UPDATE agent_sessions SET id = ? WHERE id = ?`, id, rawID); err != nil {
			t.Fatalf("renumber daemon test session %q: %v", label, err)
		}
	}

	daemonTestSessionRegistry.Lock()
	byStore[label] = id
	daemonTestSessionRegistry.Unlock()
	return id
}

// expireDaemonTestSessionLease says "this session is gone" the way the code
// now reads it: the heartbeat stopped. Its pid stays live and recorded, which
// is exactly the shape the daemon writes for every session it serves.
func expireDaemonTestSessionLease(t *testing.T, s *store.Store, agentSessionID int64) {
	t.Helper()
	lapsed := time.Now().UTC().Add(-2 * store.RuntimeLeaseDuration).Format("2006-01-02T15:04:05.000000000Z07:00")
	if _, err := s.DB().ExecContext(context.Background(),
		`UPDATE agent_sessions SET last_heartbeat_at = ? WHERE id = ?`, lapsed, agentSessionID); err != nil {
		t.Fatalf("expire lease for session %d: %v", agentSessionID, err)
	}
}
