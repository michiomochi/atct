package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

const monitorBindPollInterval = time.Second

func newMonitorToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate monitor token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func fetchMonitorBinding(ctx context.Context, client *http.Client, bases []string, token string) (store.MonitorBinding, bool, error) {
	for _, base := range bases {
		requestCtx, cancel := context.WithTimeout(ctx, watchSnapshotTimeout)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(base, "/")+"/api/monitor-bindings/"+token, nil)
		if err != nil {
			cancel()
			return store.MonitorBinding{}, false, err
		}
		response, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			response.Body.Close()
			cancel()
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			cancel()
			continue
		}
		var binding store.MonitorBinding
		err = json.NewDecoder(response.Body).Decode(&binding)
		response.Body.Close()
		cancel()
		if err != nil {
			return store.MonitorBinding{}, false, fmt.Errorf("decode monitor binding: %w", err)
		}
		return binding, true, nil
	}
	return store.MonitorBinding{}, false, nil
}

func monitorBindingScopes(binding store.MonitorBinding) []watchScope {
	assignment := binding.Assignment
	switch assignment.Role {
	case "commander":
		if assignment.ProjectID != 0 {
			return []watchScope{{Role: "commander", ProjectID: fmt.Sprint(assignment.ProjectID)}}
		}
	case "subcommander":
		if assignment.ProjectID != 0 && assignment.GoalID != 0 {
			return []watchScope{{Role: "subcommander", ProjectID: fmt.Sprint(assignment.ProjectID), GoalID: fmt.Sprint(assignment.GoalID)}}
		}
	case "executor":
		scopes := make([]watchScope, 0, len(assignment.Tasks))
		for _, task := range assignment.Tasks {
			if task.ProjectID == 0 || task.GoalID == 0 || task.TaskID == 0 {
				continue
			}
			scopes = append(scopes, watchScope{Role: "executor", ProjectID: fmt.Sprint(task.ProjectID), GoalID: fmt.Sprint(task.GoalID), TaskID: fmt.Sprint(task.TaskID)})
		}
		return scopes
	}
	return nil
}

type monitorScopeRunner func(context.Context, watchScope) error

// runMonitorBindingLoop waits for a canonical-session binding and restarts the
// harness-specific scope runner whenever the server-derived assignment changes.
func runMonitorBindingLoop(ctx context.Context, client *http.Client, urls []string, token string, runScope monitorScopeRunner) error {
	if client == nil {
		client = &http.Client{}
	}
	if runScope == nil {
		return fmt.Errorf("monitor scope runner is required")
	}
	type scopeWatch struct {
		cancel context.CancelFunc
		done   chan error
	}
	var active *scopeWatch
	stopActive := func() error {
		if active == nil {
			return nil
		}
		watch := active
		active = nil
		watch.cancel()
		var failure error
		for err := range watch.done {
			if err != nil && !errors.Is(err, context.Canceled) && failure == nil {
				failure = err
			}
		}
		return failure
	}
	startScopes := func(scopes []watchScope) {
		watchCtx, cancel := context.WithCancel(ctx)
		watch := &scopeWatch{
			cancel: cancel,
			done:   make(chan error, len(scopes)),
		}
		var group sync.WaitGroup
		group.Add(len(scopes))
		for _, scope := range scopes {
			scope := scope
			go func() {
				defer group.Done()
				watch.done <- runScope(watchCtx, scope)
			}()
		}
		go func() {
			group.Wait()
			close(watch.done)
		}()
		active = watch
	}
	current := ""
	ticker := time.NewTicker(monitorBindPollInterval)
	defer ticker.Stop()
	defer func() { _ = stopActive() }()

	for {
		binding, found, err := fetchMonitorBinding(ctx, client, urls, token)
		if err == nil && found {
			scopes := monitorBindingScopes(binding)
			for i := range scopes {
				scopes[i].MonitorToken = token
			}
			encoded, _ := json.Marshal(scopes)
			next := string(encoded)
			if next != current {
				if err := stopActive(); err != nil {
					return err
				}
				current = next
				if len(scopes) > 0 {
					startScopes(scopes)
				}
			}
		}
		var scopeDone <-chan error
		if active != nil {
			scopeDone = active.done
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-scopeDone:
			stopErr := stopActive()
			if ctx.Err() != nil {
				return nil
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			if stopErr != nil {
				return stopErr
			}
			return errors.New("monitor scope watch stopped")
		case <-ticker.C:
		}
	}
}
