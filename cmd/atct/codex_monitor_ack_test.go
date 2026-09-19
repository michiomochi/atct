package main

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexMonitorAcknowledgementsConsumeOnceAndStayLocal(t *testing.T) {
	key := monitorMutationKey{ActionClass: "task.handoff.request", HandoffID: "handoff-7", TargetID: 7}
	first, err := newCodexMonitorAcknowledgements("first-capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}
	second, err := newCodexMonitorAcknowledgements("second-capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}

	if first.Acknowledge("wrong-capability", key) {
		t.Fatal("Acknowledge with wrong capability succeeded")
	}
	if first.Acknowledge("first-capability", monitorMutationKey{}) {
		t.Fatal("Acknowledge with malformed key succeeded")
	}
	if !first.Acknowledge("first-capability", key) {
		t.Fatal("Acknowledge with valid capability failed")
	}
	if second.Consume(key) {
		t.Fatal("independent acknowledgement store consumed another store's key")
	}
	if !first.Consume(key) {
		t.Fatal("Consume did not consume acknowledged key")
	}
	if first.Consume(key) {
		t.Fatal("Consume consumed the same key twice")
	}
}

func TestCodexMonitorAcknowledgementsServeAuthenticatesWireRecords(t *testing.T) {
	store, err := newCodexMonitorAcknowledgements("capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "ack.sock"))
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- store.Serve(ctx, listener) }()

	key := monitorMutationKey{ActionClass: "task.handoff.complete", HandoffID: "handoff-9", TargetID: 9}
	if err := sendCodexMonitorAcknowledgement(listener.Addr().String(), "wrong", codexMonitorAckRecord{Type: codexMonitorAckRecordAcknowledgement, Key: key}); err != nil {
		t.Fatalf("send wrong capability: %v", err)
	}
	if store.Consume(key) {
		t.Fatal("wrong capability stored a key")
	}
	if err := sendCodexMonitorAcknowledgement(listener.Addr().String(), "capability", codexMonitorAckRecord{Type: codexMonitorAckRecordAcknowledgement}); err != nil {
		t.Fatalf("send malformed record: %v", err)
	}
	if store.Consume(key) {
		t.Fatal("malformed record stored a key")
	}
	if err := sendCodexMonitorAcknowledgement(listener.Addr().String(), "capability", codexMonitorAckRecord{Type: codexMonitorAckRecordAcknowledgement, Key: key}); err != nil {
		t.Fatalf("send valid acknowledgement: %v", err)
	}
	if !store.Consume(key) {
		t.Fatal("valid wire acknowledgement was not consumed")
	}

	cancel()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close: %v", err)
	}
	if err := <-serveDone; err != nil && err != context.Canceled {
		t.Fatalf("Serve: %v", err)
	}
}

func TestCodexMonitorAcknowledgementsArmGoalReceiptOnlyAfterMatchingLaunch(t *testing.T) {
	now := time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC)
	store, err := newCodexMonitorAcknowledgements("capability")
	if err != nil {
		t.Fatalf("newCodexMonitorAcknowledgements: %v", err)
	}
	store.now = func() time.Time { return now }
	request := monitorMutationKey{ActionClass: "goal.handoff.request", HandoffID: "goal-handoff", TargetID: 42}
	receipt := monitorMutationKey{ActionClass: "goal.handoff.receive", HandoffID: "goal-handoff", TargetID: 42}

	if store.SubcommanderLaunched("capability", 42, "goal-handoff") {
		t.Fatal("launch before request acknowledgement armed a receipt")
	}
	if !store.Acknowledge("capability", request) {
		t.Fatal("request acknowledgement failed")
	}
	if store.SubcommanderLaunched("capability", 42, "other-handoff") {
		t.Fatal("mismatched launch armed a receipt")
	}
	if !store.SubcommanderLaunched("capability", 42, "goal-handoff") {
		t.Fatal("matching launch did not arm receipt")
	}
	if store.SubcommanderLaunched("capability", 42, "goal-handoff") {
		t.Fatal("duplicate launch re-armed receipt")
	}
	if !store.Consume(receipt) {
		t.Fatal("armed receipt was not consumed")
	}
	if store.Consume(receipt) {
		t.Fatal("duplicate receipt was consumed")
	}

	if !store.Acknowledge("capability", request) {
		t.Fatal("second request acknowledgement failed")
	}
	now = now.Add(codexMonitorAckLifetime + time.Nanosecond)
	if store.SubcommanderLaunched("capability", 42, "goal-handoff") {
		t.Fatal("expired candidate armed a receipt")
	}
	if store.SubcommanderLaunched("wrong", 42, "goal-handoff") {
		t.Fatal("invalid capability armed a receipt")
	}

	if !store.Acknowledge("capability", request) {
		t.Fatal("third request acknowledgement failed")
	}
	if !store.SubcommanderLaunched("capability", 42, "goal-handoff") {
		t.Fatal("matching launch did not arm second receipt")
	}
	now = now.Add(codexMonitorAckLifetime + time.Nanosecond)
	if store.Consume(receipt) {
		t.Fatal("expired armed receipt was consumed")
	}
}
