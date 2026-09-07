package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

type fakeWatchDeliveryAPI struct {
	mu sync.Mutex

	leaseHolder string
	lease       store.OrchestrationDeliveryLease
	receipts    map[string]store.OrchestrationDeliveryReceipt
	reserveCall int
	acceptCall  int
	releaseCall int
	unknownCall int
}

func newFakeWatchDeliveryAPI() *fakeWatchDeliveryAPI {
	return &fakeWatchDeliveryAPI{receipts: make(map[string]store.OrchestrationDeliveryReceipt)}
}

func (f *fakeWatchDeliveryAPI) Acquire(_ context.Context, scopeKey, targetRole, holderMonitorID string) (store.OrchestrationDeliveryLease, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leaseHolder == "" {
		f.leaseHolder = holderMonitorID
		f.lease = store.OrchestrationDeliveryLease{ScopeKey: scopeKey, TargetRole: targetRole, HolderMonitorID: holderMonitorID, FencingToken: 1, ExpiresAt: time.Now().Add(time.Minute)}
		return f.lease, true, nil
	}
	if f.leaseHolder != holderMonitorID {
		return f.lease, false, nil
	}
	return f.lease, true, nil
}

func (f *fakeWatchDeliveryAPI) Reserve(_ context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) (store.OrchestrationDeliveryReceipt, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCall++
	key := deliveryKey + "\x00" + generation
	if receipt, ok := f.receipts[key]; ok {
		if receipt.Status == store.OrchestrationDeliveryReceiptAccepted || receipt.Status == store.OrchestrationDeliveryReceiptUnknown {
			return receipt, false, nil
		}
		return receipt, false, nil
	}
	receipt := store.OrchestrationDeliveryReceipt{ScopeKey: lease.ScopeKey, DeliveryKey: deliveryKey, Generation: generation, TargetRole: lease.TargetRole, HolderMonitorID: lease.HolderMonitorID, FencingToken: lease.FencingToken, Status: store.OrchestrationDeliveryReceiptReserved}
	f.receipts[key] = receipt
	return receipt, true, nil
}

func (f *fakeWatchDeliveryAPI) Accept(_ context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acceptCall++
	key := deliveryKey + "\x00" + generation
	receipt := f.receipts[key]
	receipt.Status = store.OrchestrationDeliveryReceiptAccepted
	receipt.HolderMonitorID = lease.HolderMonitorID
	f.receipts[key] = receipt
	return nil
}

func (f *fakeWatchDeliveryAPI) Release(_ context.Context, _ store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCall++
	delete(f.receipts, deliveryKey+"\x00"+generation)
	return nil
}

func (f *fakeWatchDeliveryAPI) Unknown(_ context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unknownCall++
	key := deliveryKey + "\x00" + generation
	receipt := f.receipts[key]
	receipt.Status = store.OrchestrationDeliveryReceiptUnknown
	receipt.HolderMonitorID = lease.HolderMonitorID
	f.receipts[key] = receipt
	return nil
}

func newWatchDeliveryTestReporter(monitorID, scopeKey string) *watchHealthReporter {
	return &watchHealthReporter{
		health:      store.MonitorHealth{MonitorID: monitorID, ScopeKey: scopeKey},
		scopeActive: true,
	}
}

func TestWatchDurableDeliverySuppressesSecondWrapperAndReconnect(t *testing.T) {
	api := newFakeWatchDeliveryAPI()
	scope := watchScope{ProjectID: "1", GoalID: "42", Role: "subcommander"}
	var delivered []watchAgentAction
	collect := func(action watchAgentAction) error {
		delivered = append(delivered, action)
		return nil
	}
	a := newWatchDurableActionSink(api, newWatchDeliveryTestReporter("monitor-a", "goal:42:subcommander:h1"), scope, collect)
	b := newWatchDurableActionSink(api, newWatchDeliveryTestReporter("monitor-b", "goal:42:subcommander:h1"), scope, collect)
	action := watchAgentAction{line: "atct monitor liveness: recheck goal 42", eventName: "monitor.liveness", goalID: "42", deliveryKey: "monitor.liveness\x00goal:42", generation: "generation-1"}

	if err := a(action); err != nil {
		t.Fatalf("first wrapper delivery: %v", err)
	}
	if err := b(action); err != nil {
		t.Fatalf("second wrapper delivery: %v", err)
	}
	if err := a(action); err != nil {
		t.Fatalf("reconnect delivery: %v", err)
	}
	if len(delivered) != 1 {
		t.Fatalf("delivered actions = %#v, want one accepted action", delivered)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.reserveCall != 2 || api.acceptCall != 1 {
		t.Fatalf("delivery calls reserve=%d accept=%d, want 2 and 1", api.reserveCall, api.acceptCall)
	}
}

func TestCodexDurableDeliveryReleasesPreSubmitAndRetries(t *testing.T) {
	api := newFakeWatchDeliveryAPI()
	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{errs: []error{errors.New("submit before acceptance"), nil}}, "thread-1")
	bridge.SetActive(true)
	sink := newWatchDurableCodexActionSink(api, newWatchDeliveryTestReporter("monitor-a", "goal:42:subcommander:h1"), watchScope{ProjectID: "1", GoalID: "42", Role: "subcommander"}, bridge)
	action := watchAgentAction{line: "atct monitor liveness: recheck goal 42", eventName: "monitor.liveness", goalID: "42", deliveryKey: "monitor.liveness\x00goal:42", generation: "generation-1"}

	if err := sink(action); err != nil {
		t.Fatalf("queue durable Codex action: %v", err)
	}
	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
		t.Fatalf("first idle notification: %v", err)
	}
	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
		t.Fatalf("retry idle notification: %v", err)
	}
	if bridge.QueueLen() != 0 {
		t.Fatalf("Codex queue length = %d, want empty after retry", bridge.QueueLen())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.releaseCall != 1 || api.acceptCall != 1 || api.unknownCall != 0 || api.reserveCall != 2 {
		t.Fatalf("delivery calls reserve=%d release=%d accept=%d unknown=%d; want 2,1,1,0", api.reserveCall, api.releaseCall, api.acceptCall, api.unknownCall)
	}
}

func TestCodexDurableDeliveryMarksUnknownWithoutRetry(t *testing.T) {
	api := newFakeWatchDeliveryAPI()
	bridge := newCodexMonitorBridge(&fakeCodexTurnStarter{errs: []error{errCodexTurnSubmitUnknown}}, "thread-1")
	bridge.SetActive(true)
	sink := newWatchDurableCodexActionSink(api, newWatchDeliveryTestReporter("monitor-a", "goal:42:subcommander:h1"), watchScope{ProjectID: "1", GoalID: "42", Role: "subcommander"}, bridge)
	action := watchAgentAction{line: "atct monitor liveness: recheck goal 42", eventName: "monitor.liveness", goalID: "42", deliveryKey: "monitor.liveness\x00goal:42", generation: "generation-1"}

	if err := sink(action); err != nil {
		t.Fatalf("queue unknown Codex action: %v", err)
	}
	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
		t.Fatalf("unknown idle notification: %v", err)
	}
	if err := bridge.HandleNotification(context.Background(), codexAppServerNotification{Method: "turn/completed", Params: mustJSON(map[string]any{"threadId": "thread-1"})}); err != nil {
		t.Fatalf("post-unknown idle notification: %v", err)
	}
	if bridge.QueueLen() != 0 {
		t.Fatalf("Codex queue length after unknown = %d, want empty", bridge.QueueLen())
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.unknownCall != 1 || api.acceptCall != 0 || api.releaseCall != 0 || api.reserveCall != 1 {
		t.Fatalf("delivery calls reserve=%d release=%d accept=%d unknown=%d; want 1,0,0,1", api.reserveCall, api.releaseCall, api.acceptCall, api.unknownCall)
	}
}
