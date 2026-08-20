package daemon

import (
	"context"
	"time"

	"github.com/michiomochi/atct/internal/store"
)

const wakeupPublishAfter = 15 * time.Minute

// wakeupTracker keeps the transition state that is intentionally not stored
// in SQLite. A condition must remain true for the grace period before it is
// published, and becoming false resets that period so a later occurrence gets
// a fresh wakeup ID.
type wakeupTracker struct {
	activeSince     map[string]time.Time
	published       map[string]bool
	discrepancySeen map[string]bool
}

func newWakeupTracker() *wakeupTracker {
	return &wakeupTracker{
		activeSince:     make(map[string]time.Time),
		published:       make(map[string]bool),
		discrepancySeen: make(map[string]bool),
	}
}

func (t *wakeupTracker) evaluate(ctx context.Context, s *store.Store, now time.Time) ([]store.DecisionEvent, error) {
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	var events []store.DecisionEvent
	for _, project := range projects {
		state, err := s.DetectWakeup(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		counted, err := s.CountUnstartedTasks(ctx, project.ID)
		if err != nil {
			return nil, err
		}

		mismatch := state.UnstartedTaskCount == 0 && counted > 0
		if mismatch {
			if !t.discrepancySeen[project.ID] {
				id := store.NewWakeupID()
				events = append(events, store.DecisionEvent{
					Name: store.EventWakeupDiscrepancy,
					Data: store.WakeupDiscrepancyEvent{
						WakeupID:                   id,
						ProjectID:                  project.ID,
						DetectorUnstartedTaskCount: state.UnstartedTaskCount,
						CountedUnstartedTaskCount:  counted,
					},
				})
				t.discrepancySeen[project.ID] = true
			}
		} else {
			delete(t.discrepancySeen, project.ID)
		}

		active := state.ActiveGoalCount > 0 && state.UnstartedTaskCount > 0
		if !active {
			delete(t.activeSince, project.ID)
			delete(t.published, project.ID)
			continue
		}

		startedAt, ok := t.activeSince[project.ID]
		if !ok {
			t.activeSince[project.ID] = now
			delete(t.published, project.ID)
			continue
		}
		if !t.published[project.ID] && !now.Before(startedAt.Add(wakeupPublishAfter)) {
			events = append(events, store.DecisionEvent{
				Name: store.EventWakeup,
				Data: store.WakeupEvent{
					WakeupID:           store.NewWakeupID(),
					ProjectID:          project.ID,
					ActiveGoalCount:    state.ActiveGoalCount,
					UnstartedTaskCount: state.UnstartedTaskCount,
					WaitingAnswerCount: state.WaitingAnswerCount,
				},
			})
			t.published[project.ID] = true
		}
	}
	return events, nil
}

func (d *Daemon) runMaintenance(ctx context.Context, tracker *wakeupTracker, now time.Time) {
	_, _ = d.store.ApplyExpiredDefaults(ctx, now)
	d.store.PublishEvent(store.DecisionEvent{
		Name: store.EventKeepalive,
		Data: store.KeepaliveEvent{At: now},
	})

	events, err := tracker.evaluate(ctx, d.store, now)
	if err != nil {
		return
	}
	for _, event := range events {
		d.store.PublishEvent(event)
	}
}
