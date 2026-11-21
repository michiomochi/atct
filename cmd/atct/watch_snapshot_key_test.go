package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// snapshotRoundTrip is a transport that fails every request with what it returns.
type snapshotRoundTrip func(*http.Request) (*http.Response, error)

func (f snapshotRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// inboxErrorFrom reads /api/inbox from the daemon on dstPort through a transport that
// fails with transportErr, and returns the error readWatchInbox reports.
func inboxErrorFrom(t *testing.T, dstPort int, transportErr error) error {
	t.Helper()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", dstPort)
	client := &http.Client{Transport: snapshotRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, transportErr
	})}
	_, err := readWatchInbox(context.Background(), client, baseURL)
	if err == nil {
		t.Fatal("readWatchInbox succeeded; want the transport error")
	}
	return err
}

// tcpOpError is the error a connection leaves when a read fails: the local port is
// new on every attempt, the remote one is the daemon's.
func tcpOpError(op string, srcPort, dstPort int, cause error) error {
	return &net.OpError{
		Op:     op,
		Net:    "tcp",
		Source: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: srcPort},
		Addr:   &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: dstPort},
		Err:    &os.SyscallError{Syscall: op, Err: cause},
	}
}

func TestWatchSnapshotResetKeyIgnoresEphemeralPort(t *testing.T) {
	first := inboxErrorFrom(t, 8787, tcpOpError("read", 51001, 8787, syscall.ECONNRESET))
	second := inboxErrorFrom(t, 8787, tcpOpError("read", 51002, 8787, syscall.ECONNRESET))

	var snapErr *watchSnapshotError
	if !errors.As(first, &snapErr) || snapErr.Kind != "reset" || snapErr.BaseURL != "http://127.0.0.1:8787" {
		t.Fatalf("first error = %#v, want a reset on 8787", first)
	}
	if !errors.Is(first, syscall.ECONNRESET) {
		t.Fatalf("reset error does not wrap ECONNRESET: %v", first)
	}
	if first.Error() == second.Error() {
		t.Fatalf("texts are equal; the test needs the port to differ: %q", first.Error())
	}
	if a, b := watchSnapshotFailureKey(first), watchSnapshotFailureKey(second); a != b {
		t.Fatalf("keys differ for one reset on two ports: %q / %q", a, b)
	}
}

func TestSnapshotFailureNotifierResetOnChangingPortSaysOnce(t *testing.T) {
	first := inboxErrorFrom(t, 8787, tcpOpError("read", 51001, 8787, syscall.ECONNRESET))
	second := inboxErrorFrom(t, 8787, tcpOpError("read", 51002, 8787, syscall.ECONNRESET))

	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var said []time.Duration
	for m := 0; m <= 32; m++ {
		err := first
		if m%2 == 1 {
			err = second
		}
		if _, due := n.Failure(t0.Add(time.Duration(m)*time.Minute), watchSnapshotFailureKey(err), err.Error()); due {
			said = append(said, time.Duration(m)*time.Minute)
		}
	}
	if want := []time.Duration{2 * time.Minute, 32 * time.Minute}; !slices.Equal(said, want) {
		t.Fatalf("lines at %v, want %v: one at two minutes, then only after thirty", said, want)
	}
}

func TestWatchLoopResetOnChangingPortNoticesOnce(t *testing.T) {
	fakeSnapshotClock(t, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		calls++
		if calls == 12 {
			cancel()
		}
		port := 51001 + calls%2
		return "", nil, inboxErrorFrom(t, 8787, tcpOpError("read", port, 8787, syscall.ECONNRESET))
	}
	var actions []watchAgentAction
	sink := func(action watchAgentAction) error {
		actions = append(actions, action)
		return nil
	}
	scope := watchScope{Role: "executor", ProjectID: "1", GoalID: "423", TaskID: "1707"}
	if err := watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(ctx, io.Discard, &http.Client{}, time.Millisecond, snapshot, nil, nil, scope, nil, sink); err != nil {
		t.Fatalf("watch loop: %v", err)
	}
	var failing int
	for _, action := range actions {
		if action.eventName == watchSnapshotFailingEvent {
			failing++
		}
	}
	if failing != 1 {
		t.Fatalf("failing notices = %d over %d resets on changing ports, want 1", failing, calls)
	}
}

func TestSnapshotNoticeDeliveryKeyFollowsKindAndAddress(t *testing.T) {
	deliveryKey := func(err error) string {
		var got watchAgentAction
		sink := func(action watchAgentAction) error {
			got = action
			return nil
		}
		scope := watchScope{Role: "executor", GoalID: "423", TaskID: "1707"}
		if err := writeWatchSnapshotNotice(io.Discard, sink, scope, watchSnapshotFailingEvent, "line", watchSnapshotFailureKey(err), "current"); err != nil {
			t.Fatal(err)
		}
		return got.deliveryKey
	}
	reset := func(src, dst int) error {
		return inboxErrorFrom(t, dst, tcpOpError("read", src, dst, syscall.ECONNRESET))
	}

	base := deliveryKey(reset(51001, 8787))
	if other := deliveryKey(reset(51002, 8787)); other != base {
		t.Fatalf("delivery key changed with the ephemeral port: %q / %q", base, other)
	}
	if timeout := deliveryKey(inboxErrorFrom(t, 8787, context.DeadlineExceeded)); timeout == base {
		t.Fatalf("a timeout shares the delivery key of a reset: %q", base)
	}
	if moved := deliveryKey(reset(51001, 8788)); moved == base {
		t.Fatalf("the same reset on another address shares its delivery key: %q", base)
	}
}

func TestSnapshotFailureNotifierKindChangeSaysAtOnce(t *testing.T) {
	timeout := inboxErrorFrom(t, 8787, context.DeadlineExceeded)
	server, _ := snapshotServer(t, http.StatusInternalServerError, "")
	_, status := readWatchInbox(context.Background(), &http.Client{}, server.URL)
	if status == nil {
		t.Fatal("readWatchInbox succeeded against a 500; want the HTTP status error")
	}

	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	n.Failure(t0, watchSnapshotFailureKey(timeout), timeout.Error())
	if _, due := n.Failure(t0.Add(2*time.Minute), watchSnapshotFailureKey(timeout), timeout.Error()); !due {
		t.Fatal("first line missing")
	}
	line, due := n.Failure(t0.Add(3*time.Minute), watchSnapshotFailureKey(status), status.Error())
	if !due || !strings.Contains(line, "HTTP status 500") {
		t.Fatalf("changed kind not said at once: line %q, due %v", line, due)
	}
}

func TestSnapshotFailureNotifierAddressChangeSaysAtOnce(t *testing.T) {
	refused := func(port int) error {
		return inboxErrorFrom(t, port, tcpOpError("dial", 51003, port, syscall.ECONNREFUSED))
	}
	on8787, on8788 := refused(8787), refused(8788)

	var n snapshotFailureNotifier
	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	n.Failure(t0, watchSnapshotFailureKey(on8787), on8787.Error())
	if _, due := n.Failure(t0.Add(2*time.Minute), watchSnapshotFailureKey(on8787), on8787.Error()); !due {
		t.Fatal("first line missing")
	}
	line, due := n.Failure(t0.Add(3*time.Minute), watchSnapshotFailureKey(on8788), on8788.Error())
	if !due || !strings.Contains(line, "8788") {
		t.Fatalf("changed address not said at once: line %q, due %v", line, due)
	}
}
