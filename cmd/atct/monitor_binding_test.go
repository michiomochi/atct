package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

func TestMonitorBindingScopesIncludesEveryExecutorTask(t *testing.T) {
	var binding store.MonitorBinding
	if err := json.Unmarshal([]byte(`{"assignment":{"role":"executor","project_id":7,"tasks":[{"project_id":7,"goal_id":16,"task_id":46},{"project_id":7,"goal_id":17,"task_id":47}]}}`), &binding); err != nil {
		t.Fatalf("decode binding fixture: %v", err)
	}
	want := []watchScope{
		{Role: "executor", ProjectID: "7", GoalID: "16", TaskID: "46"},
		{Role: "executor", ProjectID: "7", GoalID: "17", TaskID: "47"},
	}
	got := monitorBindingScopes(binding)
	if !slices.Equal(got, want) {
		t.Fatalf("monitorBindingScopes = %#v, want %#v", got, want)
	}
}

func TestFetchMonitorBindingBoundsEachRequest(t *testing.T) {
	hadDeadline := false
	client := &http.Client{Transport: watchRoundTripper(func(req *http.Request) (*http.Response, error) {
		_, hadDeadline = req.Context().Deadline()
		return nil, errors.New("daemon unavailable")
	})}

	if _, found, err := fetchMonitorBinding(context.Background(), client, []string{"http://daemon"}, "token-1"); err != nil || found {
		t.Fatalf("fetchMonitorBinding = (found %v, error %v), want retryable miss", found, err)
	}
	if !hadDeadline {
		t.Fatal("binding request had no deadline")
	}
}

func TestMonitorBindingLoopWaitsForBindingAndReplacesChangedScope(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	pendingFetched := make(chan struct{})
	thirdFetch := make(chan struct{})
	client := &http.Client{Transport: watchRoundTripper(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		requests++
		request := requests
		mu.Unlock()
		body := `{"pending":true,"assignment":{}}`
		switch request {
		case 1:
			close(pendingFetched)
		case 2:
			body = `{"pending":false,"assignment":{"role":"commander","project_id":7}}`
		default:
			body = `{"pending":false,"assignment":{"role":"subcommander","project_id":7,"goal_id":16}}`
			if request == 3 {
				close(thirdFetch)
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	started := make(chan watchScope, 2)
	commanderStopped := make(chan struct{})
	wrongOrder := make(chan struct{}, 1)
	subcommanderStopped := make(chan struct{})
	runScope := func(ctx context.Context, scope watchScope) error {
		if scope.Role == "subcommander" {
			select {
			case <-commanderStopped:
			default:
				wrongOrder <- struct{}{}
			}
		}
		started <- scope
		<-ctx.Done()
		if scope.Role == "commander" {
			time.Sleep(25 * time.Millisecond)
			close(commanderStopped)
		} else {
			close(subcommanderStopped)
		}
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runMonitorBindingLoop(ctx, client, []string{"http://daemon"}, "token-1", runScope)
	}()

	select {
	case <-pendingFetched:
	case <-time.After(time.Second):
		t.Fatal("monitor did not fetch its pending binding")
	}
	select {
	case scope := <-started:
		t.Fatalf("watch started before binding: %#v", scope)
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case scope := <-started:
		if scope != (watchScope{Role: "commander", ProjectID: "7", MonitorToken: "token-1"}) {
			t.Fatalf("first scope = %#v, want commander project 7 carrying its token", scope)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bound commander watch did not start")
	}
	select {
	case <-thirdFetch:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not refresh its binding")
	}
	select {
	case scope := <-started:
		if scope != (watchScope{Role: "subcommander", ProjectID: "7", GoalID: "16", MonitorToken: "token-1"}) {
			t.Fatalf("replacement scope = %#v, want subcommander goal 16 carrying its token", scope)
		}
	case <-time.After(time.Second):
		t.Fatal("changed binding did not start a replacement watch")
	}
	select {
	case <-wrongOrder:
		t.Fatal("replacement watch started before the old watch stopped")
	default:
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMonitorBindingLoop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("binding loop did not stop with its context")
	}
	select {
	case <-subcommanderStopped:
	case <-time.After(time.Second):
		t.Fatal("active scope watch was not stopped")
	}
}

func TestMonitorBindingLoopReturnsScopeFailureDuringAssignmentChange(t *testing.T) {
	want := errors.New("old watcher failed")
	secondFetchStarted := make(chan struct{})
	watcherReturning := make(chan struct{})
	var requests int
	client := &http.Client{Transport: watchRoundTripper(func(*http.Request) (*http.Response, error) {
		requests++
		body := `{"assignment":{"role":"commander","project_id":7}}`
		if requests == 2 {
			close(secondFetchStarted)
			<-watcherReturning
			body = `{"assignment":{"role":"subcommander","project_id":7,"goal_id":16}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	newScopeStarted := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runMonitorBindingLoop(ctx, client, []string{"http://daemon"}, "token-1", func(ctx context.Context, scope watchScope) error {
			if scope.Role == "commander" {
				<-secondFetchStarted
				close(watcherReturning)
				return want
			}
			newScopeStarted <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("runMonitorBindingLoop error = %v, want %v", err, want)
		}
	case <-newScopeStarted:
		cancel()
		err := <-done
		t.Fatalf("replacement scope started after old watcher failure; loop error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("binding loop did not observe assignment change")
	}
}

func TestMonitorBindingLoopReturnsScopeFailure(t *testing.T) {
	want := errors.New("bridge rejected action")
	client := &http.Client{Transport: watchRoundTripper(func(*http.Request) (*http.Response, error) {
		body := `{"assignment":{"role":"commander","project_id":7}}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := runMonitorBindingLoop(ctx, client, []string{"http://daemon"}, "token-1", func(context.Context, watchScope) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("runMonitorBindingLoop error = %v, want %v", err, want)
	}
}
