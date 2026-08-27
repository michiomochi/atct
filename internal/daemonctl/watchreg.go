package daemonctl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const watchReapTimeout = 5 * time.Second

// WatchScope identifies the daemon events consumed by a watch process.
type WatchScope struct {
	ProjectID string
	GoalID    string
}

// WatchRegistration records one watch process in the per-process registry.
// Legacy is true when the registration was written by an older version that
// stored only the PID as plain text.
type WatchRegistration struct {
	PID       int
	Scope     WatchScope
	StartedAt string
	Legacy    bool
}

type watchRegistrationJSON struct {
	PID       int    `json:"pid"`
	ProjectID string `json:"project_id"`
	GoalID    string `json:"goal_id"`
	StartedAt string `json:"started_at"`
}

func (r WatchRegistration) MarshalJSON() ([]byte, error) {
	return json.Marshal(watchRegistrationJSON{
		PID:       r.PID,
		ProjectID: r.Scope.ProjectID,
		GoalID:    r.Scope.GoalID,
		StartedAt: r.StartedAt,
	})
}

// RegisterWatchScoped records this process and returns a cleanup function
// that removes only its own registration.
func RegisterWatchScoped(dir string, scope WatchScope) (func(), error) {
	registryDir := filepath.Join(dir, watchRegistryDir)
	if err := os.MkdirAll(registryDir, 0o755); err != nil {
		return nil, fmt.Errorf("create watch registry: %w", err)
	}
	path := filepath.Join(registryDir, strconv.Itoa(os.Getpid()))
	registration := WatchRegistration{
		PID:       os.Getpid(),
		Scope:     scope,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(registration)
	if err != nil {
		return nil, fmt.Errorf("marshal watch registration: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("write watch registration: %w", err)
	}
	return func() { _ = os.Remove(path) }, nil
}

// ReapResult describes registrations removed or stopped while reclaiming a
// scope. Failed contains PIDs whose matching watch did not exit in time.
type ReapResult struct {
	RemovedStale int
	Stopped      []WatchRegistration
	Failed       []int
}

// ListWatches reads all non-directory entries in the watch registry. Missing
// registries are empty because a watch has not necessarily been started yet.
func ListWatches(dir string) ([]WatchRegistration, error) {
	entries, err := os.ReadDir(filepath.Join(dir, watchRegistryDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read watch registry: %w", err)
	}

	registrations := make([]WatchRegistration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, watchRegistryDir, entry.Name())
		registration, err := readWatchRegistration(path)
		if err != nil {
			continue
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

// ReapWatches removes dead registrations and stops a live watch that owns the
// same project-and-goal scope. A dead registration is removed whatever its
// format, because removing a file signals no process. A live watch is stopped
// only when its scope is known and equal: a legacy registration records no
// scope, so it is left running rather than killed on a guess.
func ReapWatches(dir string, self WatchScope, selfPID int) (ReapResult, error) {
	var result ReapResult
	if self.ProjectID == "" {
		return result, nil
	}

	registryDir := filepath.Join(dir, watchRegistryDir)
	entries, err := os.ReadDir(registryDir)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read watch registry: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(registryDir, entry.Name())
		registration, err := readWatchRegistration(path)
		if err != nil {
			continue
		}
		if !ProcessAlive(registration.PID) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, fmt.Errorf("remove stale watch registration %s: %w", entry.Name(), err)
			}
			result.RemovedStale++
			continue
		}
		if registration.Legacy {
			continue
		}
		if registration.PID == selfPID || registration.Scope != self {
			continue
		}

		process, err := os.FindProcess(registration.PID)
		if err != nil {
			result.Failed = append(result.Failed, registration.PID)
			continue
		}
		if err := process.Signal(syscall.SIGTERM); err != nil {
			result.Failed = append(result.Failed, registration.PID)
			continue
		}

		deadline := time.Now().Add(watchReapTimeout)
		for ProcessAlive(registration.PID) && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if ProcessAlive(registration.PID) {
			result.Failed = append(result.Failed, registration.PID)
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("remove stopped watch registration %s: %w", entry.Name(), err)
		}
		result.Stopped = append(result.Stopped, registration)
	}
	return result, nil
}

func readWatchRegistration(path string) (WatchRegistration, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return WatchRegistration{}, err
	}
	var record watchRegistrationJSON
	if err := json.Unmarshal(raw, &record); err == nil {
		return WatchRegistration{
			PID:       record.PID,
			Scope:     WatchScope{ProjectID: record.ProjectID, GoalID: record.GoalID},
			StartedAt: record.StartedAt,
		}, nil
	}

	pid, err := strconv.Atoi(string(bytes.TrimSpace(raw)))
	if err != nil {
		return WatchRegistration{}, fmt.Errorf("decode registration: %w", err)
	}
	return WatchRegistration{PID: pid, Legacy: true}, nil
}
