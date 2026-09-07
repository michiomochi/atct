package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/michiomochi/atct/internal/store"
)

var errWatchDeliveryNotOwner = errors.New("watch is not the current delivery owner")

type watchDeliveryAPI interface {
	Acquire(context.Context, string, string, string) (store.OrchestrationDeliveryLease, bool, error)
	Reserve(context.Context, store.OrchestrationDeliveryLease, string, string) (store.OrchestrationDeliveryReceipt, bool, error)
	Accept(context.Context, store.OrchestrationDeliveryLease, string, string) error
	Release(context.Context, store.OrchestrationDeliveryLease, string, string) error
	Unknown(context.Context, store.OrchestrationDeliveryLease, string, string) error
}

type watchHTTPDeliveryAPI struct {
	client *http.Client
	urls   []string
}

type watchDeliveryAPIRequest struct {
	Operation       string `json:"operation"`
	ScopeKey        string `json:"scope_key"`
	TargetRole      string `json:"target_role"`
	HolderMonitorID string `json:"holder_monitor_id"`
	FencingToken    int64  `json:"fencing_token"`
	DeliveryKey     string `json:"delivery_key"`
	Generation      string `json:"generation"`
}

type watchDeliveryAPIResponse struct {
	Acquired bool                                `json:"acquired"`
	Claimed  bool                                `json:"claimed"`
	Status   string                              `json:"status"`
	Lease    *store.OrchestrationDeliveryLease   `json:"lease"`
	Receipt  *store.OrchestrationDeliveryReceipt `json:"receipt"`
}

func newWatchHTTPDeliveryAPI(client *http.Client, urls []string) watchDeliveryAPI {
	if client == nil {
		client = &http.Client{}
	}
	return &watchHTTPDeliveryAPI{client: client, urls: append([]string(nil), urls...)}
}

func (a *watchHTTPDeliveryAPI) Acquire(ctx context.Context, scopeKey, targetRole, holderMonitorID string) (store.OrchestrationDeliveryLease, bool, error) {
	var response watchDeliveryAPIResponse
	err := a.call(ctx, watchDeliveryAPIRequest{Operation: "acquire", ScopeKey: scopeKey, TargetRole: targetRole, HolderMonitorID: holderMonitorID}, &response)
	if err != nil {
		return store.OrchestrationDeliveryLease{}, false, err
	}
	if response.Lease == nil {
		return store.OrchestrationDeliveryLease{}, response.Acquired, nil
	}
	return *response.Lease, response.Acquired, nil
}

func (a *watchHTTPDeliveryAPI) Reserve(ctx context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) (store.OrchestrationDeliveryReceipt, bool, error) {
	var response watchDeliveryAPIResponse
	err := a.call(ctx, watchDeliveryAPIRequest{
		Operation:       "reserve",
		ScopeKey:        lease.ScopeKey,
		TargetRole:      lease.TargetRole,
		HolderMonitorID: lease.HolderMonitorID,
		FencingToken:    lease.FencingToken,
		DeliveryKey:     deliveryKey,
		Generation:      generation,
	}, &response)
	if err != nil {
		return store.OrchestrationDeliveryReceipt{}, false, err
	}
	if response.Receipt == nil {
		return store.OrchestrationDeliveryReceipt{}, response.Claimed, errors.New("orchestration delivery reserve response has no receipt")
	}
	return *response.Receipt, response.Claimed, nil
}

func (a *watchHTTPDeliveryAPI) Accept(ctx context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	return a.settle(ctx, "accepted", lease, deliveryKey, generation)
}

func (a *watchHTTPDeliveryAPI) Release(ctx context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	return a.settle(ctx, "release", lease, deliveryKey, generation)
}

func (a *watchHTTPDeliveryAPI) Unknown(ctx context.Context, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	return a.settle(ctx, "unknown", lease, deliveryKey, generation)
}

func (a *watchHTTPDeliveryAPI) settle(ctx context.Context, operation string, lease store.OrchestrationDeliveryLease, deliveryKey, generation string) error {
	return a.call(ctx, watchDeliveryAPIRequest{
		Operation:       operation,
		ScopeKey:        lease.ScopeKey,
		TargetRole:      lease.TargetRole,
		HolderMonitorID: lease.HolderMonitorID,
		FencingToken:    lease.FencingToken,
		DeliveryKey:     deliveryKey,
		Generation:      generation,
	}, nil)
}

func (a *watchHTTPDeliveryAPI) call(ctx context.Context, payload watchDeliveryAPIRequest, response *watchDeliveryAPIResponse) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(a.urls) == 0 {
		return errors.New("no daemon HTTP addresses for orchestration delivery")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode orchestration delivery request: %w", err)
	}
	var lastErr error
	for _, baseURL := range a.urls {
		baseURL = strings.TrimRight(baseURL, "/")
		if baseURL == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/orchestration-delivery", strings.NewReader(string(body)))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		responseBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			var errorResponse struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(responseBody, &errorResponse) == nil && errorResponse.Error != "" {
				if resp.StatusCode == http.StatusConflict && strings.Contains(errorResponse.Error, "lease is not held") {
					return errWatchDeliveryNotOwner
				}
				lastErr = errors.New(errorResponse.Error)
			} else {
				lastErr = fmt.Errorf("POST %s: HTTP %s", baseURL+"/api/orchestration-delivery", resp.Status)
			}
			continue
		}
		if response != nil && len(responseBody) > 0 {
			if err := json.Unmarshal(responseBody, response); err != nil {
				lastErr = fmt.Errorf("decode orchestration delivery response: %w", err)
				continue
			}
		}
		return nil
	}
	if lastErr == nil {
		return errors.New("no usable daemon HTTP address for orchestration delivery")
	}
	return lastErr
}

type watchDeliveryHandle interface {
	reserve(context.Context) (bool, error)
	accept(context.Context) error
	release(context.Context) error
	unknown(context.Context) error
}

type watchDeliveryController struct {
	api      watchDeliveryAPI
	reporter *watchHealthReporter
	scope    watchScope
	action   watchAgentAction

	mu       sync.Mutex
	lease    store.OrchestrationDeliveryLease
	reserved bool
	settled  bool
}

func newWatchDeliveryController(api watchDeliveryAPI, reporter *watchHealthReporter, scope watchScope, action watchAgentAction) *watchDeliveryController {
	if action.deliveryKey == "" || action.generation == "" {
		action.deliveryKey, action.generation = watchActionDeliveryIdentity(action.eventName, action.line, watchDecision{GoalID: action.goalID, TargetRole: action.targetRole, ScopeKey: action.scopeKey})
	}
	return &watchDeliveryController{api: api, reporter: reporter, scope: scope, action: action}
}

func (c *watchDeliveryController) reserve(ctx context.Context) (bool, error) {
	if c == nil || c.api == nil || c.reporter == nil {
		return false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settled {
		return false, nil
	}
	if c.reserved {
		return true, nil
	}
	c.reporter.Report(ctx, "", "healthy", "delivery owner reconciliation")
	monitorID, scopeKey, ok := c.reporter.deliveryIdentity()
	if !ok {
		return false, nil
	}
	if c.action.scopeKey != "" {
		scopeKey = c.action.scopeKey
	}
	targetRole := c.action.targetRole
	if targetRole == "" {
		targetRole = c.scope.Role
	}
	if strings.TrimSpace(scopeKey) == "" || strings.TrimSpace(targetRole) == "" {
		return false, nil
	}
	lease, acquired, err := c.api.Acquire(ctx, scopeKey, targetRole, monitorID)
	if errors.Is(err, errWatchDeliveryNotOwner) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !acquired {
		return false, nil
	}
	receipt, claimed, err := c.api.Reserve(ctx, lease, c.action.deliveryKey, c.action.generation)
	if errors.Is(err, errWatchDeliveryNotOwner) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !claimed {
		if receipt.Status == store.OrchestrationDeliveryReceiptAccepted || receipt.Status == store.OrchestrationDeliveryReceiptUnknown {
			c.settled = true
		}
		return false, nil
	}
	c.lease = lease
	c.reserved = true
	return true, nil
}

func (c *watchDeliveryController) accept(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settled || !c.reserved {
		return nil
	}
	err := c.api.Accept(ctx, c.lease, c.action.deliveryKey, c.action.generation)
	if err != nil {
		c.reserved = false
		c.settled = true
		_ = c.api.Unknown(ctx, c.lease, c.action.deliveryKey, c.action.generation)
		return err
	}
	c.reserved = false
	c.settled = true
	return nil
}

func (c *watchDeliveryController) release(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.reserved {
		return nil
	}
	err := c.api.Release(ctx, c.lease, c.action.deliveryKey, c.action.generation)
	c.reserved = false
	return err
}

func (c *watchDeliveryController) unknown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.settled || !c.reserved {
		return nil
	}
	err := c.api.Unknown(ctx, c.lease, c.action.deliveryKey, c.action.generation)
	c.reserved = false
	c.settled = true
	return err
}

func newWatchDurableActionSink(api watchDeliveryAPI, reporter *watchHealthReporter, scope watchScope, sink watchAgentActionSink) watchAgentActionSink {
	if sink == nil {
		return nil
	}
	if api == nil || reporter == nil {
		return sink
	}
	return func(action watchAgentAction) error {
		controller := newWatchDeliveryController(api, reporter, scope, action)
		claimed, err := controller.reserve(context.Background())
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		if err := sink(action); err != nil {
			_ = controller.release(context.Background())
			return err
		}
		return controller.accept(context.Background())
	}
}

func newWatchDurableCodexActionSink(api watchDeliveryAPI, reporter *watchHealthReporter, scope watchScope, bridge *codexMonitorBridge) watchAgentActionSink {
	if bridge == nil {
		return nil
	}
	return func(action watchAgentAction) error {
		if api == nil || reporter == nil {
			return bridge.enqueueAction(context.Background(), codexMonitorAction{line: action.line, eventName: action.eventName, goalID: action.goalID})
		}
		controller := newWatchDeliveryController(api, reporter, scope, action)
		claimed, err := controller.reserve(context.Background())
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}
		if err := bridge.enqueueAction(context.Background(), codexMonitorAction{line: action.line, eventName: action.eventName, goalID: action.goalID, delivery: controller}); err != nil {
			bridge.stateMu.Lock()
			disabled := bridge.disabled
			bridge.stateMu.Unlock()
			if !disabled {
				return nil
			}
			_ = controller.release(context.Background())
			return err
		}
		return nil
	}
}

func watchActionDeliveryIdentity(eventName, line string, decision watchDecision) (string, string) {
	subject := ""
	switch {
	case eventName == "orchestration.recovery":
		subject = decision.BlockerID
		if subject == "" {
			subject = decision.SourceID
		}
		if subject == "" {
			subject = decision.ScopeKey
		}
		if subject == "" {
			subject = decision.HandoffID
		}
		if subject == "" {
			subject = decision.TaskID
		}
		if subject == "" {
			subject = decision.GoalID
		}
		subject = strings.Join([]string{decision.Condition, subject}, "\x00")
	case strings.HasPrefix(eventName, "decision."):
		subject = decision.DecisionID
	case strings.Contains(eventName, ".handoff."):
		subject = decision.HandoffID
		if subject == "" {
			subject = decision.TaskID
		}
		if subject == "" {
			subject = decision.GoalID
		}
	case strings.HasPrefix(eventName, "detection."):
		subject = decision.DetectionID
		if subject == "" {
			subject = decision.DecisionID
		}
		if subject == "" {
			subject = decision.HandoffID
		}
		if subject == "" {
			subject = decision.TaskID
		}
		if subject == "" {
			subject = decision.GoalID
		}
	case eventName == "goal.created", eventName == "monitor.liveness":
		subject = decision.GoalID
		if eventName == "monitor.liveness" && decision.TaskID != "" {
			subject = decision.TaskID
		}
	case strings.HasPrefix(eventName, "wakeup"):
		subject = decision.wakeupID()
	}
	if subject == "" {
		subject = line
	}
	deliveryKey := strings.Join([]string{eventName, decision.TargetRole, subject}, "\x00")
	generation := decision.deliveryGeneration
	if generation == "" {
		generation = decision.Generation
	}
	if generation == "" {
		if strings.HasPrefix(eventName, "decision.") {
			generation = fmt.Sprintf("default:%t", decision.defaultApplied())
		} else if eventName == "wakeup" && decision.WakeupID != "" {
			generation = decision.WakeupID
		} else {
			generation = "current"
		}
	}
	return deliveryKey, generation
}

func (r *watchHealthReporter) deliveryIdentity() (monitorID, scopeKey string, ok bool) {
	if r == nil {
		return "", "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.scopeActive || strings.TrimSpace(r.health.MonitorID) == "" || strings.TrimSpace(r.health.ScopeKey) == "" {
		return "", "", false
	}
	return r.health.MonitorID, r.health.ScopeKey, true
}
