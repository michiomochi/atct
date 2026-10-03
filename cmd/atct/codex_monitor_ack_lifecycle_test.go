package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/daemonctl"
)

func TestParseArgsCodexMonitorSubcommanderHandoff(t *testing.T) {
	cfg, err := parseArgs([]string{
		"codex", "monitor",
		"--role", "subcommander",
		"--goal", "248",
		"--handoff", "goal-handoff-248",
		"--", "--model", "gpt-5",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.codexMonitorExplicit || cfg.codexMonitorRole != "subcommander" || cfg.codexMonitorGoalID != "248" || cfg.codexMonitorHandoffID != "goal-handoff-248" {
		t.Fatalf("monitor config = %#v, want explicit subcommander goal/handoff", cfg)
	}
	if want := []string{"--model", "gpt-5"}; !slices.Equal(cfg.codexArgs, want) {
		t.Fatalf("Codex args = %#v, want %#v", cfg.codexArgs, want)
	}
}

func TestParseArgsCodexMonitorRejectsIncompleteSubcommanderHandoff(t *testing.T) {
	for _, args := range [][]string{
		{"codex", "monitor", "--handoff", "goal-handoff-248"},
		{"codex", "monitor", "--role", "subcommander", "--goal", "248"},
		{"codex", "monitor", "--role", "subcommander", "--handoff", "goal-handoff-248"},
		{"codex", "monitor", "--role", "commander", "--handoff", "goal-handoff-248"},
	} {
		t.Run(strings.Join(args[2:], "_"), func(t *testing.T) {
			if _, err := parseArgs(args); !errors.Is(err, errInvalidArgs) {
				t.Fatalf("parseArgs(%q) error = %v, want errInvalidArgs", args, err)
			}
		})
	}
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "a")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestCodexMonitorCommanderAckEnvironmentIsTUIOnlyAndCleanedUp(t *testing.T) {
	monitorDir := shortSocketDir(t)
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	var appEnv, tuiEnv []string
	deps := codexMonitorDeps{
		resolveCodex:    func() (string, error) { return "/opt/codex", nil },
		newMonitorToken: func() (string, error) { return "token-commander", nil },
		reap:            func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register:        func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, env []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				appEnv = append([]string(nil), env...)
				return app, nil
			}
			tuiEnv = append([]string(nil), env...)
			tui.finish()
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:   "monitor",
		codexMonitorExplicit: true,
		codexMonitorRole:     "commander",
	}, monitorDir, deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if environmentValue(appEnv, codexMonitorAckSocketEnvironment) != "" || environmentValue(appEnv, codexMonitorAckCapabilityEnvironment) != "" {
		t.Fatalf("App Server received acknowledgement environment: %#v", appEnv)
	}
	ackSocket := environmentValue(tuiEnv, codexMonitorAckSocketEnvironment)
	if ackSocket == "" || environmentValue(tuiEnv, codexMonitorAckCapabilityEnvironment) == "" {
		t.Fatalf("TUI environment = %#v, want acknowledgement socket and capability", tuiEnv)
	}
	if _, err := os.Stat(ackSocket); !os.IsNotExist(err) {
		t.Fatalf("acknowledgement socket stat error = %v, want removed socket", err)
	}
}

func TestCodexMonitorCommanderAckListenFailureIsReportedAndLaunchContinues(t *testing.T) {
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	var tuiEnv []string
	var stderr strings.Builder
	deps := codexMonitorDeps{
		resolveCodex:    func() (string, error) { return "/opt/codex", nil },
		newMonitorToken: func() (string, error) { return "token-commander", nil },
		reap:            func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register:        func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, env []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return app, nil
			}
			tuiEnv = append([]string(nil), env...)
			tui.finish()
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		listenUnix: func(string, string) (net.Listener, error) { return nil, errors.New("listen refused") },
		stderr:     &stderr,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:   "monitor",
		codexMonitorExplicit: true,
		codexMonitorRole:     "commander",
	}, shortSocketDir(t), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "acknowledgement disabled") {
		t.Fatalf("stderr = %q, want acknowledgement disabled notice", stderr.String())
	}
	if environmentValue(tuiEnv, codexMonitorAckSocketEnvironment) != "" || environmentValue(tuiEnv, codexMonitorAckCapabilityEnvironment) != "" {
		t.Fatalf("TUI environment = %#v, want no acknowledgement environment", tuiEnv)
	}
}

func TestCodexMonitorSubcommanderLaunchAcknowledgementUsesExactIDsAfterTUISetup(t *testing.T) {
	const (
		parentSocket     = "/tmp/parent-ack.sock"
		parentCapability = "parent-capability"
	)
	t.Setenv(codexMonitorAckSocketEnvironment, parentSocket)
	t.Setenv(codexMonitorAckCapabilityEnvironment, parentCapability)
	app := newFakeCodexMonitorApp()
	tui := newFakeCodexMonitorProcess(0)
	var records []codexMonitorAckRecord
	var appEnv, tuiEnv []string
	var parentEnvironmentAtStart []string
	deps := codexMonitorDeps{
		resolveCodex:    func() (string, error) { return "/opt/codex", nil },
		newMonitorToken: func() (string, error) { return "token-subcommander", nil },
		runNormal:       func(string, []string) (int, error) { return 0, nil },
		reap:            func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		register:        func(string, daemonctl.CodexMonitorRecord) (func(), error) { return func() {}, nil },
		sendAcknowledgement: func(address, capability string, record codexMonitorAckRecord) error {
			if address != parentSocket || capability != parentCapability {
				t.Fatalf("launch acknowledgement destination = (%q, %q), want (%q, %q)", address, capability, parentSocket, parentCapability)
			}
			records = append(records, record)
			return nil
		},
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, env []string) (codexMonitorProcess, error) {
			parentEnvironmentAtStart = append(parentEnvironmentAtStart,
				os.Getenv(codexMonitorAckSocketEnvironment),
				os.Getenv(codexMonitorAckCapabilityEnvironment),
			)
			if kind == codexMonitorAppServer {
				appEnv = append([]string(nil), env...)
				return app, nil
			}
			tuiEnv = append([]string(nil), env...)
			tui.finish()
			return tui, nil
		},
		connectAppServer: func(context.Context, string) (codexMonitorApp, error) { return app, nil },
		projectPath:      func() (string, error) { return "/project", nil },
		runBoundWatch: func(ctx context.Context, _ string, _ string, _ *codexMonitorBridge) error {
			<-ctx.Done()
			return nil
		},
		stderr: io.Discard,
	}

	code, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:    "monitor",
		codexMonitorExplicit:  true,
		codexMonitorRole:      "subcommander",
		codexMonitorGoalID:    "248",
		codexMonitorHandoffID: "goal-handoff-248",
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(records) != 1 || records[0].Type != codexMonitorAckRecordSubcommanderLaunched || records[0].GoalID != 248 || records[0].HandoffID != "goal-handoff-248" {
		t.Fatalf("launch records = %#v, want one exact launch record", records)
	}
	if len(parentEnvironmentAtStart) != 4 || slices.Contains(parentEnvironmentAtStart, parentSocket) || slices.Contains(parentEnvironmentAtStart, parentCapability) {
		t.Fatalf("parent acknowledgement environment at child start = %#v, want removed", parentEnvironmentAtStart)
	}
	if environmentValue(appEnv, codexMonitorAckSocketEnvironment) != "" || environmentValue(tuiEnv, codexMonitorAckCapabilityEnvironment) != "" {
		t.Fatalf("child process acknowledgement environment leaked: app=%#v tui=%#v", appEnv, tuiEnv)
	}
}

func TestCodexMonitorSubcommanderLaunchAcknowledgementWaitsForSetup(t *testing.T) {
	t.Setenv(codexMonitorAckSocketEnvironment, "/tmp/parent-ack.sock")
	t.Setenv(codexMonitorAckCapabilityEnvironment, "parent-capability")
	var sends int
	deps := codexMonitorDeps{
		resolveCodex:    func() (string, error) { return "/opt/codex", nil },
		newMonitorToken: func() (string, error) { return "token-subcommander", nil },
		runNormal:       func(string, []string) (int, error) { return 0, nil },
		reap:            func(string) (daemonctl.CodexMonitorReapResult, error) { return daemonctl.CodexMonitorReapResult{}, nil },
		sendAcknowledgement: func(string, string, codexMonitorAckRecord) error {
			sends++
			return nil
		},
		startProcess: func(kind codexMonitorProcessKind, _ string, _ []string, _ []string) (codexMonitorProcess, error) {
			if kind == codexMonitorAppServer {
				return nil, errors.New("App Server unavailable")
			}
			return nil, errors.New("TUI must not start")
		},
		projectPath: func() (string, error) { return "/project", nil },
		stderr:      io.Discard,
	}

	_, err := runCodexMonitorWithDeps(cliConfig{
		codexMonitorAction:    "monitor",
		codexMonitorExplicit:  true,
		codexMonitorRole:      "subcommander",
		codexMonitorGoalID:    "248",
		codexMonitorHandoffID: "goal-handoff-248",
	}, t.TempDir(), deps)
	if err != nil {
		t.Fatalf("runCodexMonitorWithDeps: %v", err)
	}
	if sends != 0 {
		t.Fatalf("launch acknowledgements = %d, want 0 after setup failure", sends)
	}
}

func TestCodexMonitorAcknowledgementGatesWatchQueueBeforeEnqueue(t *testing.T) {
	store, err := newCodexMonitorAcknowledgements("capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}
	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-1", store)
	bridge.SetActive(true)
	action := watchAgentAction{
		line:      "atct task handoff requested (task_id: 7, handoff_id: handoff-7)",
		eventName: "task.handoff.request",
		taskID:    "7",
		handoffID: "handoff-7",
	}
	key, ok := codexMonitorMutationKeyFromWatchAction(action)
	if !ok {
		t.Fatal("codexMonitorMutationKeyFromWatchAction returned no key")
	}
	if !store.Acknowledge("capability", key) {
		t.Fatal("Acknowledge failed")
	}
	if err := bridge.ActionSink()(action); err != nil {
		t.Fatalf("ActionSink acknowledged action: %v", err)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue length after acknowledged action = %d, want 0", got)
	}
	if err := bridge.ActionSink()(action); err != nil {
		t.Fatalf("ActionSink second action: %v", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("queue length after second action = %d, want 1", got)
	}
}

func TestCodexMonitorAcknowledgementWireSuppressesBridgeAction(t *testing.T) {
	store, err := newCodexMonitorAcknowledgements("capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}
	listener, err := net.Listen("unix", filepath.Join(shortSocketDir(t), "ack.sock"))
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- store.Serve(ctx, listener) }()

	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-1", store)
	bridge.SetActive(true)
	key := monitorMutationKey{ActionClass: "task.handoff.request", HandoffID: "handoff-7", TargetID: 7}
	if err := sendCodexMonitorAcknowledgement(listener.Addr().String(), "capability", codexMonitorAckRecord{
		Type: codexMonitorAckRecordAcknowledgement,
		Key:  key,
	}); err != nil {
		t.Fatalf("send acknowledgement: %v", err)
	}
	action := watchAgentAction{
		line:      "atct task handoff requested (task_id: 7, handoff_id: handoff-7)",
		eventName: "task.handoff.request",
		taskID:    "7",
		handoffID: "handoff-7",
	}
	if err := bridge.ActionSink()(action); err != nil {
		t.Fatalf("ActionSink acknowledged action: %v", err)
	}
	if got := bridge.QueueLen(); got != 0 {
		t.Fatalf("queue length after wire acknowledgement = %d, want 0", got)
	}
	if err := bridge.ActionSink()(action); err != nil {
		t.Fatalf("ActionSink duplicate action: %v", err)
	}
	if got := bridge.QueueLen(); got != 1 {
		t.Fatalf("queue length after duplicate action = %d, want 1", got)
	}

	cancel()
	_ = listener.Close()
	if err := <-serveDone; err != nil && err != context.Canceled {
		t.Fatalf("Serve: %v", err)
	}
}

func TestCodexMonitorLocalMutationOrderingAndIsolation(t *testing.T) {
	taskAction := watchAgentAction{
		line:      "atct task handoff requested (task_id: 7, handoff_id: handoff-7)",
		eventName: "task.handoff.request",
		taskID:    "7",
		handoffID: "handoff-7",
	}
	taskKey := monitorMutationKey{ActionClass: "task.handoff.request", HandoffID: "handoff-7", TargetID: 7}

	newBridge := func(t *testing.T, store *codexMonitorAcknowledgements) *codexMonitorBridge {
		t.Helper()
		bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-1", store)
		bridge.SetActive(true)
		return bridge
	}

	t.Run("acknowledgement_before_action_suppresses_once", func(t *testing.T) {
		store, err := newCodexMonitorAcknowledgements("capability")
		if err != nil {
			t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
		}
		if !store.Acknowledge("capability", taskKey) {
			t.Fatal("Acknowledge failed")
		}
		bridge := newBridge(t, store)
		if err := bridge.ActionSink()(taskAction); err != nil {
			t.Fatalf("ActionSink first action: %v", err)
		}
		if got := bridge.QueueLen(); got != 0 {
			t.Fatalf("queue length after acknowledged action = %d, want 0", got)
		}
		if err := bridge.ActionSink()(taskAction); err != nil {
			t.Fatalf("ActionSink duplicate action: %v", err)
		}
		if got := bridge.QueueLen(); got != 1 {
			t.Fatalf("queue length after duplicate action = %d, want 1", got)
		}
	})

	t.Run("action_before_ack_is_delivered", func(t *testing.T) {
		store, err := newCodexMonitorAcknowledgements("capability")
		if err != nil {
			t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
		}
		bridge := newBridge(t, store)
		if err := bridge.ActionSink()(taskAction); err != nil {
			t.Fatalf("ActionSink: %v", err)
		}
		if !store.Acknowledge("capability", taskKey) {
			t.Fatal("Acknowledge failed")
		}
		if got := bridge.QueueLen(); got != 1 {
			t.Fatalf("queue length after action-before-ack = %d, want 1", got)
		}
	})

	t.Run("same_ids_different_action_class_is_delivered", func(t *testing.T) {
		store, err := newCodexMonitorAcknowledgements("capability")
		if err != nil {
			t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
		}
		if !store.Acknowledge("capability", taskKey) {
			t.Fatal("Acknowledge failed")
		}
		bridge := newBridge(t, store)
		if err := bridge.ActionSink()(watchAgentAction{
			line:      "atct task handoff completed (task_id: 7, handoff_id: handoff-7)",
			eventName: "task.handoff.complete",
			taskID:    "7",
			handoffID: "handoff-7",
		}); err != nil {
			t.Fatalf("ActionSink: %v", err)
		}
		if got := bridge.QueueLen(); got != 1 {
			t.Fatalf("queue length after action-class mismatch = %d, want 1", got)
		}
	})

	t.Run("foreign_store_cannot_suppress", func(t *testing.T) {
		local, err := newCodexMonitorAcknowledgements("local")
		if err != nil {
			t.Fatalf("new local acknowledgements: %v", err)
		}
		foreign, err := newCodexMonitorAcknowledgements("foreign")
		if err != nil {
			t.Fatalf("new foreign acknowledgements: %v", err)
		}
		if !foreign.Acknowledge("foreign", taskKey) {
			t.Fatal("foreign Acknowledge failed")
		}
		bridge := newBridge(t, local)
		if err := bridge.ActionSink()(taskAction); err != nil {
			t.Fatalf("ActionSink: %v", err)
		}
		if got := bridge.QueueLen(); got != 1 {
			t.Fatalf("queue length after foreign acknowledgement = %d, want 1", got)
		}
	})

	for _, tc := range []struct {
		name   string
		action watchAgentAction
		key    monitorMutationKey
	}{
		{
			name: "goal",
			action: watchAgentAction{
				line:      "atct goal handoff requested (goal_id: 248, handoff_id: goal-handoff)",
				eventName: "goal.handoff.request",
				goalID:    "248",
				handoffID: "goal-handoff",
			},
			key: monitorMutationKey{ActionClass: "goal.handoff.request", HandoffID: "goal-handoff", TargetID: 248},
		},
		{
			name: "plan",
			action: watchAgentAction{
				line:      "atct plan handoff review requested (goal_id: 248, handoff_id: plan-handoff)",
				eventName: "plan.handoff.review.request",
				goalID:    "248",
				handoffID: "plan-handoff",
			},
			key: monitorMutationKey{ActionClass: "plan.handoff.review.request", HandoffID: "plan-handoff", TargetID: 248},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := newCodexMonitorAcknowledgements("capability")
			if err != nil {
				t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
			}
			if !store.Acknowledge("capability", tc.key) {
				t.Fatal("Acknowledge failed")
			}
			bridge := newBridge(t, store)
			if err := bridge.ActionSink()(tc.action); err != nil {
				t.Fatalf("ActionSink: %v", err)
			}
			if got := bridge.QueueLen(); got != 0 {
				t.Fatalf("queue length after exact %s acknowledgement = %d, want 0", tc.name, got)
			}
		})
	}
}

func TestCodexMonitorDelegatedLifecycleOrderingAtBridge(t *testing.T) {
	request := monitorMutationKey{ActionClass: "goal.handoff.request", HandoffID: "goal-handoff", TargetID: 248}
	receipt := watchAgentAction{
		line:      "atct goal handoff received (goal_id: 248, handoff_id: goal-handoff)",
		eventName: "goal.handoff.receive",
		goalID:    "248",
		handoffID: "goal-handoff",
	}

	newCase := func(t *testing.T) (*codexMonitorAcknowledgements, *codexMonitorBridge) {
		t.Helper()
		store, err := newCodexMonitorAcknowledgements("capability")
		if err != nil {
			t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
		}
		bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{}, "thread-1", store)
		bridge.SetActive(true)
		return store, bridge
	}

	t.Run("request_then_launch_then_receipt_suppresses_once", func(t *testing.T) {
		store, bridge := newCase(t)
		if !store.Acknowledge("capability", request) || !store.SubcommanderLaunched("capability", 248, "goal-handoff") {
			t.Fatal("request/launch correlation failed")
		}
		if err := bridge.ActionSink()(receipt); err != nil {
			t.Fatalf("ActionSink first receipt: %v", err)
		}
		if got := bridge.QueueLen(); got != 0 {
			t.Fatalf("queue length after correlated receipt = %d, want 0", got)
		}
		if err := bridge.ActionSink()(receipt); err != nil {
			t.Fatalf("ActionSink duplicate receipt: %v", err)
		}
		if got := bridge.QueueLen(); got != 1 {
			t.Fatalf("queue length after duplicate receipt = %d, want 1", got)
		}
	})

	for _, tc := range []struct {
		name  string
		setup func(*codexMonitorAcknowledgements) error
	}{
		{name: "receipt_before_launch", setup: func(*codexMonitorAcknowledgements) error { return nil }},
		{name: "launch_before_request", setup: func(store *codexMonitorAcknowledgements) error {
			if store.SubcommanderLaunched("capability", 248, "goal-handoff") {
				return errors.New("launch unexpectedly armed receipt")
			}
			return nil
		}},
		{name: "request_without_successful_child_setup", setup: func(store *codexMonitorAcknowledgements) error {
			if !store.Acknowledge("capability", request) {
				return errors.New("request acknowledgement failed")
			}
			return nil
		}},
		{name: "mismatched_child_launch", setup: func(store *codexMonitorAcknowledgements) error {
			if !store.Acknowledge("capability", request) || store.SubcommanderLaunched("capability", 248, "other-handoff") {
				return errors.New("mismatched launch changed lifecycle state")
			}
			return nil
		}},
		{name: "independent_capability", setup: func(store *codexMonitorAcknowledgements) error {
			if store.SubcommanderLaunched("other-capability", 248, "goal-handoff") {
				return errors.New("foreign capability armed receipt")
			}
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, bridge := newCase(t)
			if err := tc.setup(store); err != nil {
				t.Fatal(err)
			}
			if err := bridge.ActionSink()(receipt); err != nil {
				t.Fatalf("ActionSink: %v", err)
			}
			if got := bridge.QueueLen(); got != 1 {
				t.Fatalf("queue length after %s = %d, want 1", tc.name, got)
			}
		})
	}
}

func TestCodexMonitorMutationKeyParserRequiresExactWatchTuple(t *testing.T) {
	tests := []struct {
		name        string
		eventName   string
		line        string
		wantAction  string
		wantTarget  int64
		wantHandoff string
		want        bool
	}{
		{
			name:        "task handoff",
			eventName:   "task.handoff.request",
			line:        "atct task handoff requested (task_id: 7, handoff_id: handoff-7)",
			wantAction:  "task.handoff.request",
			wantTarget:  7,
			wantHandoff: "handoff-7",
			want:        true,
		},
		{
			name:        "goal handoff",
			eventName:   "goal.handoff.receive",
			line:        "atct goal handoff received (goal_id: 248, handoff_id: goal-handoff-248)",
			wantAction:  "goal.handoff.receive",
			wantTarget:  248,
			wantHandoff: "goal-handoff-248",
			want:        true,
		},
		{
			name:      "unsupported action",
			eventName: "goal.review.complete",
			line:      "atct goal review approved (goal_id: 248, decision_id: 71)",
			want:      false,
		},
		{
			name:      "missing target identity",
			eventName: "task.handoff.request",
			line:      "atct task handoff requested (handoff_id: handoff-7)",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := watchAgentAction{eventName: tt.eventName, line: tt.line}
			got, ok := codexMonitorMutationKeyFromWatchAction(action)
			if ok != tt.want {
				t.Fatalf("key = %#v, ok = %v, want ok %v", got, ok, tt.want)
			}
			if !tt.want {
				return
			}
			if got.ActionClass != tt.wantAction || got.TargetID != tt.wantTarget || got.HandoffID != tt.wantHandoff {
				t.Fatalf("key = %#v, want (%q, %q, %d)", got, tt.wantAction, tt.wantHandoff, tt.wantTarget)
			}
		})
	}
}

func environmentValue(environment []string, name string) string {
	prefix := name + "="
	for _, entry := range environment {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
