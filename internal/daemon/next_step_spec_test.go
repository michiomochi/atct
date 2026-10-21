package daemon

import (
	"testing"

	"github.com/michiomochi/atct/internal/store"
)

// Every call the dashboard's next action names must be one next_step offers,
// so an agent told "call X" finds X in the table. A missing one means the
// dashboard and next_step disagree about the goal's next step.
func TestNextActionCallsAreOfferedByNextStep(t *testing.T) {
	calls := map[string]bool{}
	for _, spec := range store.NextActionSpecs {
		if spec.Call != "" {
			calls[spec.Call] = true
		}
	}
	// Kept until stage 2 closes the task handoff machinery; next_step no longer
	// sends an executor there, but the dashboard spec stays for open handoffs.
	delete(calls, "atct_task_create_handoff_receive")
	if got := missing(calls, tableTools()); len(got) > 0 {
		t.Fatalf("store.NextActionSpecs names %v, which no next_step offers", got)
	}
}
