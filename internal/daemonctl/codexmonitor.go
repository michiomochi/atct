package daemonctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const codexMonitorRegistryName = "codex-monitors"

var (
	codexMonitorStopTimeout        = 5 * time.Second
	codexMonitorPollInterval       = 20 * time.Millisecond
	codexMonitorStartTimeTolerance = 10 * time.Second
)

// CodexMonitorRecord describes one explicit Codex monitor supervisor and the
// App Server child it owns.
type CodexMonitorRecord struct {
	SupervisorPID int    `json:"supervisor_pid"`
	AppServerPID  int    `json:"app_server_pid"`
	SocketPath    string `json:"socket_path"`
	ProjectPath   string `json:"project_path"`
	StartedAt     string `json:"started_at"`
}

// CodexMonitorRegistration is kept as a descriptive alias for callers that
// use the same record terminology as the normal daemon registry.
type CodexMonitorRegistration = CodexMonitorRecord

// CodexMonitorReapResult describes records reclaimed before a new monitor is
// started or while an explicit stop is being processed.
type CodexMonitorReapResult struct {
	RemovedStale     int
	RemovedMalformed int
	Stopped          []CodexMonitorRecord
	Failed           []int
}

// CodexMonitorStopResult describes monitors matched by an exact project path.
type CodexMonitorStopResult struct {
	Stopped          []CodexMonitorRecord
	RemovedMalformed int
	Failed           []int
}

func CodexMonitorRegistryDir(dir string) string {
	return filepath.Join(dir, codexMonitorRegistryName)
}

func CodexMonitorRecordPath(dir string, supervisorPID int) string {
	return filepath.Join(CodexMonitorRegistryDir(dir), strconv.Itoa(supervisorPID)+".json")
}

// RegisterCodexMonitor atomically writes a monitor record and returns a
// cleanup function that can remove only that record.
func RegisterCodexMonitor(dir string, record CodexMonitorRecord) (func(), error) {
	if err := writeCodexMonitorRecord(dir, record); err != nil {
		return nil, err
	}
	return func() { _ = RemoveCodexMonitor(dir, record.SupervisorPID) }, nil
}

// WriteCodexMonitorRecord writes or replaces a record without changing any
// other monitor registration.
func WriteCodexMonitorRecord(dir string, record CodexMonitorRecord) error {
	return writeCodexMonitorRecord(dir, record)
}

func writeCodexMonitorRecord(dir string, record CodexMonitorRecord) error {
	if err := validateCodexMonitorRecord(dir, record); err != nil {
		return err
	}
	registryDir := CodexMonitorRegistryDir(dir)
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		return fmt.Errorf("create Codex monitor registry: %w", err)
	}
	if err := os.Chmod(registryDir, 0o700); err != nil {
		return fmt.Errorf("protect Codex monitor registry: %w", err)
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Codex monitor record: %w", err)
	}
	tmp, err := os.CreateTemp(registryDir, ".codex-monitor-*.tmp")
	if err != nil {
		return fmt.Errorf("create Codex monitor record: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		_ = tmp.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("protect Codex monitor record: %w", err)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write Codex monitor record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync Codex monitor record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close Codex monitor record: %w", err)
	}
	path := CodexMonitorRecordPath(dir, record.SupervisorPID)
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install Codex monitor record: %w", err)
	}
	removeTemp = false
	return nil
}

// RemoveCodexMonitor removes one record selected by its numeric supervisor
// PID. The path is generated below the managed registry directory, so a
// caller cannot use this operation to remove an arbitrary file.
func RemoveCodexMonitor(dir string, supervisorPID int) error {
	if supervisorPID <= 0 {
		return fmt.Errorf("Codex monitor supervisor PID must be positive: %d", supervisorPID)
	}
	if err := os.Remove(CodexMonitorRecordPath(dir, supervisorPID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Codex monitor record: %w", err)
	}
	return nil
}

// ListCodexMonitors returns valid records. Malformed entries are left for
// ReapCodexMonitors, which removes them as part of a lifecycle operation.
func ListCodexMonitors(dir string) ([]CodexMonitorRecord, error) {
	entries, err := os.ReadDir(CodexMonitorRegistryDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex monitor registry: %w", err)
	}
	records := make([]CodexMonitorRecord, 0, len(entries))
	for _, entry := range entries {
		if !isCodexMonitorRecordEntry(entry) {
			continue
		}
		record, err := readCodexMonitorRecord(filepath.Join(CodexMonitorRegistryDir(dir), entry.Name()))
		if err != nil {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].StartedAt == records[j].StartedAt {
			return records[i].SupervisorPID < records[j].SupervisorPID
		}
		return records[i].StartedAt < records[j].StartedAt
	})
	return records, nil
}

// ReapCodexMonitors removes malformed and dead-supervisor records. If a dead
// supervisor left its recorded App Server alive, only that child is signaled.
func ReapCodexMonitors(dir string) (CodexMonitorReapResult, error) {
	var result CodexMonitorReapResult
	entries, err := os.ReadDir(CodexMonitorRegistryDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read Codex monitor registry: %w", err)
	}
	for _, entry := range entries {
		if !isCodexMonitorRecordEntry(entry) {
			continue
		}
		path := filepath.Join(CodexMonitorRegistryDir(dir), entry.Name())
		record, err := readCodexMonitorRecord(path)
		if err != nil {
			if removeErr := removeCodexMonitorPath(path); removeErr != nil {
				return result, removeErr
			}
			result.RemovedMalformed++
			continue
		}
		if codexMonitorProcessAlive(record.SupervisorPID) {
			continue
		}
		if record.AppServerPID > 0 && codexMonitorProcessAlive(record.AppServerPID) {
			if err := stopCodexMonitorProcess(record.AppServerPID); err != nil {
				result.Failed = append(result.Failed, record.SupervisorPID)
				continue
			}
		}
		if err := removeCodexMonitorSocket(record); err != nil {
			return result, err
		}
		if err := removeCodexMonitorPath(path); err != nil {
			return result, err
		}
		result.RemovedStale++
		result.Stopped = append(result.Stopped, record)
	}
	return result, nil
}

// StopCodexMonitorsForProject stops live monitor supervisors whose recorded
// project path exactly equals projectPath. Other projects and malformed/live
// records outside the requested scope are left untouched.
func StopCodexMonitorsForProject(dir, projectPath string) (CodexMonitorStopResult, error) {
	var result CodexMonitorStopResult
	reaped, err := ReapCodexMonitors(dir)
	if err != nil {
		return result, err
	}
	result.RemovedMalformed = reaped.RemovedMalformed
	result.Failed = append(result.Failed, reaped.Failed...)
	entries, err := os.ReadDir(CodexMonitorRegistryDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read Codex monitor registry: %w", err)
	}
	projectPath = filepath.Clean(projectPath)
	for _, entry := range entries {
		if !isCodexMonitorRecordEntry(entry) {
			continue
		}
		path := filepath.Join(CodexMonitorRegistryDir(dir), entry.Name())
		record, err := readCodexMonitorRecord(path)
		if err != nil {
			continue
		}
		if filepath.Clean(record.ProjectPath) != projectPath || !codexMonitorProcessAlive(record.SupervisorPID) {
			continue
		}
		if !codexMonitorProcessMatchesRecord(record) {
			result.Failed = append(result.Failed, record.SupervisorPID)
			continue
		}
		if err := stopCodexMonitorProcess(record.SupervisorPID); err != nil {
			result.Failed = append(result.Failed, record.SupervisorPID)
			continue
		}
		if err := removeCodexMonitorSocket(record); err != nil {
			return result, err
		}
		if err := removeCodexMonitorPath(path); err != nil {
			return result, err
		}
		result.Stopped = append(result.Stopped, record)
	}
	return result, nil
}

func codexMonitorProcessMatchesRecord(record CodexMonitorRecord) bool {
	want, err := time.Parse(time.RFC3339Nano, record.StartedAt)
	if err != nil {
		return false
	}
	actual, err := CodexMonitorProcessStartTime(record.SupervisorPID)
	if err != nil {
		return false
	}
	delta := want.Sub(actual)
	return delta >= -codexMonitorStartTimeTolerance && delta <= codexMonitorStartTimeTolerance
}

// StopCodexMonitors is a concise compatibility wrapper for callers that use
// the operation name rather than its project-scoped form.
func StopCodexMonitors(dir, projectPath string) (CodexMonitorStopResult, error) {
	return StopCodexMonitorsForProject(dir, projectPath)
}

func readCodexMonitorRecord(path string) (CodexMonitorRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return CodexMonitorRecord{}, err
	}
	var record CodexMonitorRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return CodexMonitorRecord{}, fmt.Errorf("decode Codex monitor record: %w", err)
	}
	if err := validateCodexMonitorRecord(filepath.Dir(filepath.Dir(path)), record); err != nil {
		return CodexMonitorRecord{}, err
	}
	return record, nil
}

func isCodexMonitorRecordEntry(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json")
}

func removeCodexMonitorSocket(record CodexMonitorRecord) error {
	if err := os.Remove(record.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Codex monitor socket: %w", err)
	}
	return nil
}

func validateCodexMonitorRecord(dir string, record CodexMonitorRecord) error {
	if record.SupervisorPID <= 0 {
		return fmt.Errorf("Codex monitor supervisor PID must be positive: %d", record.SupervisorPID)
	}
	if record.AppServerPID < 0 {
		return fmt.Errorf("Codex monitor App Server PID must not be negative: %d", record.AppServerPID)
	}
	if strings.TrimSpace(record.SocketPath) == "" || !filepath.IsAbs(record.SocketPath) {
		return errors.New("Codex monitor socket path must be absolute")
	}
	if !pathWithin(CodexMonitorRegistryDir(dir), record.SocketPath) {
		return fmt.Errorf("Codex monitor socket path %q is outside managed registry", record.SocketPath)
	}
	if strings.TrimSpace(record.ProjectPath) == "" || !filepath.IsAbs(record.ProjectPath) {
		return errors.New("Codex monitor project path must be absolute")
	}
	if strings.TrimSpace(record.StartedAt) == "" {
		return errors.New("Codex monitor start time is empty")
	}
	if _, err := time.Parse(time.RFC3339Nano, record.StartedAt); err != nil {
		return fmt.Errorf("parse Codex monitor start time: %w", err)
	}
	return nil
}

func pathWithin(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && relative != "."
}

func removeCodexMonitorPath(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Codex monitor record: %w", err)
	}
	return nil
}

var codexMonitorProcessAlive = ProcessAlive

func stopCodexMonitorProcess(pid int) error {
	if pid <= 0 || !codexMonitorProcessAlive(pid) {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find Codex monitor process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal Codex monitor process %d: %w", pid, err)
	}
	deadline := time.Now().Add(codexMonitorStopTimeout)
	for codexMonitorProcessAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(codexMonitorPollInterval)
	}
	if codexMonitorProcessAlive(pid) {
		return fmt.Errorf("Codex monitor process %d did not exit within %s", pid, codexMonitorStopTimeout)
	}
	return nil
}
