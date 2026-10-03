package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// watchOnceGrace lets the actions of one reconcile reach stdout together
// before the watch ends.
const watchOnceGrace = 500 * time.Millisecond

// watchOnce ends a watch after its first actionable batch. A re-armed watch
// reconciles from scratch and would emit every open action again, so what was
// delivered is kept per token on disk and skipped by the next process.
type watchOnce struct {
	path   string
	cancel context.CancelFunc
	now    func() time.Time

	mu       sync.Mutex
	seen     map[string]int64
	timerSet bool
	fired    atomic.Bool
}

func newWatchOnce(dir, token string, cancel context.CancelFunc, now func() time.Time) *watchOnce {
	digest := sha256.Sum256([]byte(token))
	w := &watchOnce{
		path:   filepath.Join(dir, "watch-once-"+hex.EncodeToString(digest[:])+".json"),
		cancel: cancel,
		now:    now,
		seen:   map[string]int64{},
	}
	// A missing, empty, or corrupt record is an empty record.
	if raw, err := os.ReadFile(w.path); err == nil {
		var seen map[string]int64
		if json.Unmarshal(raw, &seen) == nil && seen != nil {
			w.seen = seen
		}
	}
	return w
}

// Fired reports whether any action reached stdout.
func (w *watchOnce) Fired() bool { return w.fired.Load() }

func (w *watchOnce) Sink(next watchAgentActionSink) watchAgentActionSink {
	return func(action watchAgentAction) error {
		if action.controlOnly {
			return next(action)
		}
		key := action.deliveryKey + "\x00" + action.generation
		w.mu.Lock()
		defer w.mu.Unlock()
		if at, ok := w.seen[key]; ok {
			// Only the liveness line is allowed to repeat, once its interval is up.
			if action.eventName != "monitor.liveness" || w.now().Sub(time.Unix(at, 0)) < watchLivenessRepeatInterval {
				return nil
			}
		}
		if err := next(action); err != nil {
			return err
		}
		w.fired.Store(true)
		w.seen[key] = w.now().Unix()
		if err := w.save(); err != nil {
			fmt.Fprintf(os.Stderr, "atct watch: record delivered action: %v\n", err)
		}
		if !w.timerSet {
			w.timerSet = true
			time.AfterFunc(watchOnceGrace, w.cancel)
		}
		return nil
	}
}

func (w *watchOnce) save() error {
	raw, err := json.Marshal(w.seen)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.path), filepath.Base(w.path)+".tmp*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(raw)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(tmp.Name(), w.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
