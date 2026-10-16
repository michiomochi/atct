package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// snapshotRefusedURL returns an address where nothing listens, so a dial is refused.
func snapshotRefusedURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return "http://" + addr
}

// snapshotServer answers /api/inbox with status and body and counts the hits.
func snapshotServer(t *testing.T, status int, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func TestFetchWatchSnapshotTimeoutDoesNotFallThrough(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(watchSnapshotTimeout + 2*time.Second):
		}
	}))
	t.Cleanup(slow.Close)
	alive, hits := snapshotServer(t, http.StatusOK, `{"unapplied_decisions":[]}`)

	_, _, err := fetchWatchSnapshot(context.Background(), &http.Client{}, []string{slow.URL, alive.URL})
	if err == nil {
		t.Fatal("fetchWatchSnapshot succeeded; want the timeout of the first address")
	}
	if !strings.Contains(err.Error(), "snapshot timeout") || !strings.Contains(err.Error(), slow.URL+"/api/inbox") {
		t.Fatalf("error = %v; want snapshot timeout naming %s/api/inbox", err, slow.URL)
	}
	if strings.Contains(err.Error(), "refused") {
		t.Fatalf("error = %v; a timeout must not be reported as refused", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("alive address was requested %d times; a timeout must not fall through", n)
	}
}

func TestFetchWatchSnapshotSkipsRefusedAddress(t *testing.T) {
	alive, _ := snapshotServer(t, http.StatusOK, `{"unapplied_decisions":[]}`)

	base, _, err := fetchWatchSnapshot(context.Background(), &http.Client{}, []string{snapshotRefusedURL(t), alive.URL})
	if err != nil {
		t.Fatalf("fetchWatchSnapshot: %v", err)
	}
	if base != alive.URL {
		t.Fatalf("base = %q, want %q", base, alive.URL)
	}
}

func TestFetchWatchSnapshotStopsAtHTTPError(t *testing.T) {
	broken, _ := snapshotServer(t, http.StatusInternalServerError, "")
	alive, hits := snapshotServer(t, http.StatusOK, `{"unapplied_decisions":[]}`)

	_, _, err := fetchWatchSnapshot(context.Background(), &http.Client{}, []string{broken.URL, alive.URL})
	if err == nil || !strings.Contains(err.Error(), "HTTP status 500") || !strings.Contains(err.Error(), broken.URL+"/api/inbox") {
		t.Fatalf("error = %v; want HTTP status 500 naming %s/api/inbox", err, broken.URL)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("alive address was requested %d times; an HTTP error must not fall through", n)
	}
}

func TestFetchWatchSnapshotReportsRefusedWhenNothingListens(t *testing.T) {
	_, _, err := fetchWatchSnapshot(context.Background(), &http.Client{}, []string{snapshotRefusedURL(t)})
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error = %v; want connection refused", err)
	}
}
