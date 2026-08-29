package httpapi

import (
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

func TestEventMatchesTaskIDSuppressesTasklessEvaluateFailureAndKeepsKeepalive(t *testing.T) {
	if !eventMatchesTaskID(store.DecisionEvent{
		Name: store.EventKeepalive,
		Data: store.KeepaliveEvent{At: time.Now()},
	}, 46) {
		t.Fatal("task filter suppressed keepalive, want true")
	}

	if eventMatchesTaskID(store.DecisionEvent{
		Name: store.EventWakeupEvaluateFailed,
		Data: store.WakeupEvaluateFailedEvent{WakeupID: "wakeup-1", Reason: "database unavailable"},
	}, 46) {
		t.Fatal("task filter delivered taskless wakeup.evaluate_failed, want false")
	}
}
