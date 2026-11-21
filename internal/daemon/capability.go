package daemon

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// MonitoredCallerCapabilityTTL is deliberately short. A capability is a
	// one-request proof for a monitored CLI invocation, not a durable session
	// credential.
	MonitoredCallerCapabilityTTL = 30 * time.Second

	capabilityTokenBytes = 32
)

var (
	ErrMonitoredCallerRequired = errors.New("monitored caller capability is required")
	ErrCapabilityInvalid       = errors.New("invalid monitored caller capability")
	ErrCapabilityExpired       = errors.New("monitored caller capability expired")
	ErrCapabilityReplay        = errors.New("monitored caller capability replay rejected")
	ErrCapabilityPeerMismatch  = errors.New("monitored caller capability is bound to another socket peer")
)

type monitoredCallerCapability struct {
	AgentSessionID int64
	ExpiresAt      time.Time
	PeerID         uint64
	Used           bool
}

type monitoredCallerCapabilityResponse struct {
	Capability     string    `json:"capability"`
	AgentSessionID int64     `json:"agent_session_id"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func newMonitoredCallerCapability(now time.Time, agentSessionID int64, peerID uint64) (string, monitoredCallerCapability, error) {
	raw := make([]byte, capabilityTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", monitoredCallerCapability{}, fmt.Errorf("generate monitored caller capability: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, monitoredCallerCapability{
		AgentSessionID: agentSessionID,
		ExpiresAt:      now.Add(MonitoredCallerCapabilityTTL),
		PeerID:         peerID,
	}, nil
}

func (d *Daemon) issueMonitoredCallerCapability(agentSessionID int64, peerID uint64) (monitoredCallerCapabilityResponse, error) {
	if agentSessionID <= 0 {
		return monitoredCallerCapabilityResponse{}, fmt.Errorf("%w: agent_session_id must be positive", ErrMonitoredCallerRequired)
	}
	token, capability, err := newMonitoredCallerCapability(d.clock().UTC(), agentSessionID, peerID)
	if err != nil {
		return monitoredCallerCapabilityResponse{}, err
	}
	d.capabilityMu.Lock()
	if d.capabilities == nil {
		d.capabilities = make(map[string]monitoredCallerCapability)
	}
	d.capabilities[token] = capability
	d.capabilityMu.Unlock()
	return monitoredCallerCapabilityResponse{
		Capability:     token,
		AgentSessionID: agentSessionID,
		ExpiresAt:      capability.ExpiresAt,
	}, nil
}

func (d *Daemon) consumeMonitoredCallerCapability(token string, peerID uint64) (int64, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, ErrMonitoredCallerRequired
	}
	now := d.clock().UTC()
	d.capabilityMu.Lock()
	defer d.capabilityMu.Unlock()
	capability, ok := d.capabilities[token]
	if !ok {
		return 0, ErrCapabilityInvalid
	}
	if capability.Used {
		return 0, ErrCapabilityReplay
	}
	if !now.Before(capability.ExpiresAt) {
		return 0, ErrCapabilityExpired
	}
	if capability.PeerID != peerID {
		return 0, ErrCapabilityPeerMismatch
	}
	capability.Used = true
	d.capabilities[token] = capability
	return capability.AgentSessionID, nil
}

func capabilityValue(capability, callerCapability, monitorCapability string) string {
	for _, value := range []string{capability, callerCapability, monitorCapability} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// capabilityFields is shared by the handoff RPCs. The aliases let older
// monitored launchers carry the same opaque token without exposing a second
// authorization path.
type capabilityFields struct {
	Capability        string `json:"capability"`
	CallerCapability  string `json:"caller_capability"`
	MonitorCapability string `json:"monitor_capability"`
}

func (d *Daemon) sessionFromCapability(fields capabilityFields, agentSessionID int64, peerID uint64) (int64, error) {
	token := capabilityValue(fields.Capability, fields.CallerCapability, fields.MonitorCapability)
	if token == "" {
		return agentSessionID, nil
	}
	capabilitySessionID, err := d.consumeMonitoredCallerCapability(token, peerID)
	if err != nil {
		return 0, err
	}
	if agentSessionID != 0 && agentSessionID != capabilitySessionID {
		return 0, fmt.Errorf("%w: capability belongs to session %d, request named session %d", ErrCapabilityPeerMismatch, capabilitySessionID, agentSessionID)
	}
	return capabilitySessionID, nil
}
