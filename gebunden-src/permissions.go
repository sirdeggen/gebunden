package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PermissionRequest represents a request for user permission to perform an action.
type PermissionRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type,omitempty"`
	App       string                 `json:"app"`
	Origin    string                 `json:"origin,omitempty"`
	Message   string                 `json:"message"`
	Amount    int64                  `json:"amount,omitempty"`
	Asset     string                 `json:"asset,omitempty"`
	Timestamp int64                  `json:"timestamp,omitempty"`
	ExtraData map[string]interface{} `json:"extra_data,omitempty"`
}

// PermissionGate defines an interface to obtain user consent for actions.
type PermissionGate interface {
	RequestPermission(req PermissionRequest) (bool, error)
}

// BridgePermissionGate proxies permission prompts to the Gebunden Bridge service.
// The bridge handles the actual user interaction (Telegram, WhatsApp, etc.).
type BridgePermissionGate struct {
	bridgeURL   string
	autoApprove bool
	client      *http.Client
}

// NewBridgePermissionGate creates a new permission gate that talks to the bridge.
// bridgeURL is the base URL of the bridge service (e.g. http://localhost:18789).
func NewBridgePermissionGate(bridgeURL string, autoApprove bool) *BridgePermissionGate {
	return &BridgePermissionGate{
		bridgeURL:   bridgeURL,
		autoApprove: autoApprove,
		client: &http.Client{
			Timeout: 130 * time.Second, // slightly longer than bridge's 120s timeout
		},
	}
}

// RequestPermission sends the permission request to the bridge and blocks until
// the user approves or denies (or the bridge times out).
func (g *BridgePermissionGate) RequestPermission(req PermissionRequest) (bool, error) {
	if g == nil {
		return true, nil
	}
	if g.autoApprove {
		return true, nil
	}

	// Ensure timestamp
	if req.Timestamp == 0 {
		req.Timestamp = time.Now().Unix()
	}

	body, err := json.Marshal(req)
	if err != nil {
		return false, fmt.Errorf("failed to marshal permission request: %w", err)
	}

	url := g.bridgeURL + "/request-permission"
	resp, err := g.client.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		// Bridge unreachable — deny by default for safety
		return false, fmt.Errorf("bridge unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusGatewayTimeout {
		return false, fmt.Errorf("permission request timed out (user did not respond)")
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("bridge returned status %d", resp.StatusCode)
	}

	var result struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode bridge response: %w", err)
	}

	return result.Approved, nil
}
