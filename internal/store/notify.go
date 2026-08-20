package store

import (
	"context"
	"sync"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

type notifier struct {
	mu        sync.Mutex
	subs      map[string][]chan struct{}
	broadcast map[chan struct{}]struct{}
	events    map[chan DecisionEvent]struct{}
}

// DecisionEvent describes a committed store transition for broadcast
// consumers such as the human-facing SSE endpoint. Data is deliberately
// open-ended so the same event bus can carry decisions, wakeups, and daemon
// keepalives.
type DecisionEvent struct {
	Name string
	Data any
}

// Event is the generic name for a DecisionEvent. The alias keeps the event
// bus readable at call sites that publish non-decision events.
type Event = DecisionEvent

func newNotifier() *notifier {
	return &notifier{
		subs:      make(map[string][]chan struct{}),
		broadcast: make(map[chan struct{}]struct{}),
		events:    make(map[chan DecisionEvent]struct{}),
	}
}

func (n *notifier) subscribe(decisionID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.subs[decisionID] = append(n.subs[decisionID], ch)
	n.mu.Unlock()

	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		list := n.subs[decisionID]
		for i, c := range list {
			if c == ch {
				n.subs[decisionID] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(n.subs[decisionID]) == 0 {
			delete(n.subs, decisionID)
		}
	}
}

func (n *notifier) publish(decisionID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.subs[decisionID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (n *notifier) subscribeAll() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	n.broadcast[ch] = struct{}{}
	n.mu.Unlock()

	return ch, func() {
		n.mu.Lock()
		delete(n.broadcast, ch)
		n.mu.Unlock()
	}
}

func (n *notifier) publishAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.broadcast {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (n *notifier) subscribeEvents() (<-chan DecisionEvent, func()) {
	ch := make(chan DecisionEvent, 16)
	n.mu.Lock()
	n.events[ch] = struct{}{}
	n.mu.Unlock()

	return ch, func() {
		n.mu.Lock()
		delete(n.events, ch)
		n.mu.Unlock()
	}
}

func (n *notifier) publishEvent(event DecisionEvent) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.events {
		select {
		case ch <- event:
		default:
		}
	}
}

// SubscribeEvents subscribes to committed store events. Events are delivered
// on a buffered channel and slow subscribers may miss events; publishing
// never blocks the store or other subscribers.
func (s *Store) SubscribeEvents() (<-chan DecisionEvent, func()) {
	return s.notify.subscribeEvents()
}

// SubscribeDecisionEvents preserves the old API name while the event stream
// now carries more than Decision transitions.
func (s *Store) SubscribeDecisionEvents() (<-chan DecisionEvent, func()) {
	return s.SubscribeEvents()
}

// PublishEvent broadcasts a committed daemon event to stream consumers.
func (s *Store) PublishEvent(event DecisionEvent) {
	s.notify.publishEvent(event)
}

// WaitForAnswer waits for an answer and returns ok=false when timeout parks it.
// Subscribe before reading the current value; the reverse order can miss an answer
// that arrives between the read and the subscription.
func (s *Store) WaitForAnswer(ctx context.Context, decisionID string, timeout time.Duration) (domain.Decision, bool, error) {
	ch, cancel := s.notify.subscribe(decisionID)
	defer cancel()

	d, err := s.GetDecision(ctx, decisionID)
	if err != nil {
		return domain.Decision{}, false, err
	}
	if d.Status != domain.DecisionOpen {
		return d, true, nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		d, err := s.GetDecision(ctx, decisionID)
		if err != nil {
			return domain.Decision{}, false, err
		}
		return d, d.Status != domain.DecisionOpen, nil
	case <-timer.C:
		return domain.Decision{}, false, nil
	case <-ctx.Done():
		return domain.Decision{}, false, ctx.Err()
	}
}
