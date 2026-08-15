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
}

func newNotifier() *notifier {
	return &notifier{
		subs:      make(map[string][]chan struct{}),
		broadcast: make(map[chan struct{}]struct{}),
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
