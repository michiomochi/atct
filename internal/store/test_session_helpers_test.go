package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

var testSessionRegistry = struct {
	sync.Mutex
	ids  map[string]int64
	next int64
}{ids: make(map[string]int64), next: 100000}

func testSessionID(label string) int64 {
	testSessionRegistry.Lock()
	defer testSessionRegistry.Unlock()
	if id, ok := testSessionRegistry.ids[label]; ok {
		return id
	}
	testSessionRegistry.next++
	testSessionRegistry.ids[label] = testSessionRegistry.next
	return testSessionRegistry.next
}

func registerNamedTestAgentSession(t *testing.T, s *Store, label string, pid int) int64 {
	t.Helper()
	id := testSessionID(label)
	storedPID := 0
	startedAt := ""
	if actualStartedAt, err := processStartedAt(pid); err == nil {
		storedPID = pid
		startedAt = actualStartedAt
	}
	registeredAt := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.DB().ExecContext(context.Background(), `
		INSERT OR IGNORE INTO agent_sessions (id, pid, started_at, registered_at)
		VALUES (?, ?, ?, ?)
	`, id, storedPID, startedAt, registeredAt); err != nil {
		t.Fatalf("register test agent session %q: %v", label, err)
	}
	// These tests say "this session is running" by handing in a live pid, and
	// "this one is gone" by handing in a dead one or none. Liveness is a lease
	// now, so say the same thing in the terms the code reads.
	if storedPID != 0 {
		if err := s.HeartbeatAgentSession(context.Background(), id, time.Now().UTC()); err != nil {
			t.Fatalf("lease test agent session %q: %v", label, err)
		}
	}
	return id
}

func requireNamedTestAgentSession(t *testing.T, s *Store, label string) int64 {
	t.Helper()
	id := testSessionID(label)
	var count int
	if err := s.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM agent_sessions WHERE id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("find test agent session %q: %v", label, err)
	}
	if count == 0 {
		t.Fatalf("test agent session %q is not registered", label)
	}
	return id
}

func testSessionLabel(id int64) string {
	return fmt.Sprintf("session-%d", id)
}

func testSessionRef(value any) int64 {
	switch value := value.(type) {
	case int64:
		return value
	case string:
		return testSessionID(value)
	default:
		panic(fmt.Sprintf("unsupported test agent session reference %T", value))
	}
}

func nullableTestSessionRef(value any) any {
	if value == nil {
		return nil
	}
	return testSessionRef(value)
}

// expireTestAgentSessionLease makes a session stale. Tests used to do this by
// killing the process they had registered, back when liveness asked the
// operating system about a pid; the lease is what answers now, so let it
// lapse instead.
func expireTestAgentSessionLease(t *testing.T, s *Store, agentSessionID int64) {
	t.Helper()
	lapsed := time.Now().UTC().Add(-RuntimeLeaseDuration - time.Second)
	if err := s.HeartbeatAgentSession(context.Background(), agentSessionID, lapsed); err != nil {
		t.Fatalf("expire lease for agent session %d: %v", agentSessionID, err)
	}
}
