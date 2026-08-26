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
