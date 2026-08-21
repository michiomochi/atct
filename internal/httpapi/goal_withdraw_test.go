package httpapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestHTTPWithdrawActiveGoalRejectsDroppedGoal(t *testing.T) {
	f := newBareFixture(t)
	if err := f.store.WithdrawActiveGoal(f.ctx, f.goal.ID, "first withdrawal"); err != nil {
		t.Fatalf("WithdrawActiveGoal: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodPost, fmt.Sprintf("%s/api/goals/%s/withdraw", srv.URL, f.goal.ID), mustJSON(t, map[string]string{
		"reason": "second withdrawal",
	}))
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", status, http.StatusConflict, body)
	}
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, body)
	}
	want := fmt.Sprintf("goal %s is dropped, not active", f.goal.ID)
	if response["error"] != want {
		t.Fatalf("error = %q, want %q", response["error"], want)
	}
}
