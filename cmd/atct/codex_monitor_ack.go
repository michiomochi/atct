package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	codexMonitorAckLifetime                   = 5 * time.Minute
	codexMonitorAckDeadline                   = time.Second
	codexMonitorAckRecordAcknowledgement      = "acknowledgement"
	codexMonitorAckRecordSubcommanderLaunched = "subcommander-launched"
	codexMonitorAckSocketEnvironment          = "ATCT_MONITOR_ACK_SOCKET"
	codexMonitorAckCapabilityEnvironment      = "ATCT_MONITOR_ACK_CAPABILITY"
)

type monitorMutationKey struct {
	ActionClass string `json:"action_class"`
	HandoffID   string `json:"handoff_id"`
	TargetID    int64  `json:"target_id"`
}

type codexMonitorAckRecord struct {
	Type      string             `json:"type"`
	Key       monitorMutationKey `json:"key,omitempty"`
	GoalID    int64              `json:"goal_id,omitempty"`
	HandoffID string             `json:"handoff_id,omitempty"`
}

type codexMonitorAckEnvelope struct {
	Capability string                `json:"capability"`
	Record     codexMonitorAckRecord `json:"record"`
}

type codexMonitorHandoffPair struct {
	GoalID    int64
	HandoffID string
}

type codexMonitorAcknowledgements struct {
	capability string

	mu           sync.Mutex
	acknowledged map[monitorMutationKey]struct{}
	candidates   map[codexMonitorHandoffPair]time.Time
	armed        map[monitorMutationKey]time.Time
	now          func() time.Time
}

func newCodexMonitorAcknowledgements(capability string) (*codexMonitorAcknowledgements, error) {
	if capability == "" {
		return nil, errors.New("monitor acknowledgement capability is required")
	}
	return &codexMonitorAcknowledgements{
		capability:   capability,
		acknowledged: make(map[monitorMutationKey]struct{}),
		candidates:   make(map[codexMonitorHandoffPair]time.Time),
		armed:        make(map[monitorMutationKey]time.Time),
		now:          time.Now,
	}, nil
}

func newCodexMonitorAcknowledgementCapability() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate monitor acknowledgement capability: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func (a *codexMonitorAcknowledgements) Acknowledge(capability string, key monitorMutationKey) bool {
	if a == nil || !a.validCapability(capability) || !key.valid() {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.currentTime()
	a.pruneLocked(now)
	a.acknowledged[key] = struct{}{}
	if key.ActionClass == "goal.handoff.request" {
		a.candidates[codexMonitorHandoffPair{GoalID: key.TargetID, HandoffID: key.HandoffID}] = now.Add(codexMonitorAckLifetime)
	}
	return true
}

func (a *codexMonitorAcknowledgements) Consume(key monitorMutationKey) bool {
	if a == nil || !key.valid() {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.pruneLocked(a.currentTime())
	if _, ok := a.acknowledged[key]; ok {
		delete(a.acknowledged, key)
		return true
	}
	if _, ok := a.armed[key]; ok {
		delete(a.armed, key)
		return true
	}
	return false
}

func (a *codexMonitorAcknowledgements) SubcommanderLaunched(capability string, goalID int64, handoffID string) bool {
	if a == nil || !a.validCapability(capability) || goalID <= 0 || strings.TrimSpace(handoffID) == "" {
		return false
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.currentTime()
	a.pruneLocked(now)
	pair := codexMonitorHandoffPair{GoalID: goalID, HandoffID: handoffID}
	if _, ok := a.candidates[pair]; !ok {
		return false
	}
	delete(a.candidates, pair)
	a.armed[monitorMutationKey{ActionClass: "goal.handoff.receive", HandoffID: handoffID, TargetID: goalID}] = now.Add(codexMonitorAckLifetime)
	return true
}

func (a *codexMonitorAcknowledgements) Serve(ctx context.Context, listener net.Listener) error {
	if a == nil {
		return errors.New("monitor acknowledgements are required")
	}
	if listener == nil {
		return errors.New("monitor acknowledgement listener is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-stop:
		}
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		a.handleConnection(connection)
	}
}

func (a *codexMonitorAcknowledgements) handleConnection(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(codexMonitorAckDeadline))
	var envelope codexMonitorAckEnvelope
	decoder := json.NewDecoder(io.LimitReader(connection, 64<<10))
	if err := decoder.Decode(&envelope); err != nil || !a.validCapability(envelope.Capability) {
		return
	}
	switch envelope.Record.Type {
	case codexMonitorAckRecordAcknowledgement:
		a.Acknowledge(envelope.Capability, envelope.Record.Key)
	case codexMonitorAckRecordSubcommanderLaunched:
		a.SubcommanderLaunched(envelope.Capability, envelope.Record.GoalID, envelope.Record.HandoffID)
	}
}

func sendCodexMonitorAcknowledgement(address, capability string, record codexMonitorAckRecord) error {
	connection, err := net.DialTimeout("unix", address, codexMonitorAckDeadline)
	if err != nil {
		return fmt.Errorf("dial monitor acknowledgement listener: %w", err)
	}
	defer connection.Close()
	_ = connection.SetWriteDeadline(time.Now().Add(codexMonitorAckDeadline))
	if err := json.NewEncoder(connection).Encode(codexMonitorAckEnvelope{Capability: capability, Record: record}); err != nil {
		return fmt.Errorf("send monitor acknowledgement: %w", err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(codexMonitorAckDeadline))
	if _, err := io.Copy(io.Discard, connection); err != nil {
		return fmt.Errorf("wait for monitor acknowledgement: %w", err)
	}
	return nil
}

func (a *codexMonitorAcknowledgements) validCapability(capability string) bool {
	return a != nil && subtle.ConstantTimeCompare([]byte(a.capability), []byte(capability)) == 1
}

func (a *codexMonitorAcknowledgements) currentTime() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

func (a *codexMonitorAcknowledgements) pruneLocked(now time.Time) {
	for pair, expiresAt := range a.candidates {
		if !now.Before(expiresAt) {
			delete(a.candidates, pair)
		}
	}
	for key, expiresAt := range a.armed {
		if !now.Before(expiresAt) {
			delete(a.armed, key)
		}
	}
}

func (key monitorMutationKey) valid() bool {
	if key.TargetID <= 0 || strings.TrimSpace(key.HandoffID) == "" {
		return false
	}
	_, ok := codexMonitorActionClasses[key.ActionClass]
	return ok
}

func codexMonitorMutationKeyFromWatchAction(action watchAgentAction) (monitorMutationKey, bool) {
	actionClass := strings.TrimSpace(action.eventName)
	handoffID := strings.TrimSpace(action.handoffID)
	if handoffID == "" {
		handoffID = watchActionHandoffID(action.eventName, action.deliveryKey, action.line)
	}
	targetText := ""
	switch {
	case actionClass == "handoff_reported":
		if strings.Contains(action.line, "handoff reported: task ") {
			actionClass = "handoff_reported.task"
			targetText = action.taskID
			if targetText == "" {
				targetText = watchActionField(action.line, "handoff reported: task ", " ")
			}
		} else if strings.Contains(action.line, "handoff reported: goal ") {
			actionClass = "handoff_reported.goal"
			targetText = action.goalID
			if targetText == "" {
				targetText = watchActionField(action.line, "handoff reported: goal ", " ")
			}
		}
	case strings.HasPrefix(actionClass, "task."):
		targetText = action.taskID
		if targetText == "" {
			targetText = watchActionField(action.line, "task_id: ", ",")
		}
	case strings.HasPrefix(actionClass, "goal."), strings.HasPrefix(actionClass, "plan."):
		targetText = action.goalID
		if targetText == "" {
			targetText = watchActionField(action.line, "goal_id: ", ",")
		}
	}
	if handoffID == "" {
		return monitorMutationKey{}, false
	}
	targetID, err := strconv.ParseInt(strings.TrimSpace(targetText), 10, 64)
	if err != nil {
		return monitorMutationKey{}, false
	}
	key := monitorMutationKey{ActionClass: actionClass, HandoffID: handoffID, TargetID: targetID}
	return key, key.valid()
}

func watchActionHandoffID(eventName, deliveryKey, line string) string {
	parts := strings.Split(deliveryKey, "\x00")
	if len(parts) == 3 && parts[0] == eventName && strings.Contains(parts[0], ".handoff.") && strings.TrimSpace(parts[2]) != "" {
		return strings.TrimSpace(parts[2])
	}
	if value := watchActionField(line, "handoff_id: ", ","); value != "" {
		return strings.TrimSuffix(value, ")")
	}
	const marker = "(handoff "
	if start := strings.Index(line, marker); start >= 0 {
		value := line[start+len(marker):]
		if end := strings.IndexByte(value, ')'); end >= 0 {
			return strings.TrimSpace(value[:end])
		}
	}
	return ""
}

func watchActionField(line, marker, endMarker string) string {
	start := strings.Index(line, marker)
	if start < 0 {
		return ""
	}
	value := line[start+len(marker):]
	if endMarker != "" {
		if end := strings.Index(value, endMarker); end >= 0 {
			value = value[:end]
		}
	}
	return strings.TrimSpace(value)
}

var codexMonitorActionClasses = map[string]struct{}{
	"task.handoff.request":        {},
	"task.handoff.receive":        {},
	"task.handoff.complete":       {},
	"task.handoff.review.request": {},
	"task.handoff.review.receive": {},
	"task.handoff.review.reject":  {},
	"handoff_reported.task":       {},
	"goal.handoff.request":        {},
	"goal.handoff.receive":        {},
	"goal.handoff.complete":       {},
	"goal.handoff.review.request": {},
	"goal.handoff.review.receive": {},
	"goal.handoff.review.reject":  {},
	"handoff_reported.goal":       {},
	"plan.handoff.review.request": {},
	"plan.handoff.review.receive": {},
	"plan.handoff.review.reject":  {},
}
