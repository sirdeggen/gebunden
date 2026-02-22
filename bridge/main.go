package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type PermissionRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // spend, protocol, basket, certificate, group, counterparty
	App       string                 `json:"app"`
	Origin    string                 `json:"origin"`
	Message   string                 `json:"message"`
	Amount    int64                  `json:"amount,omitempty"`
	Asset     string                 `json:"asset,omitempty"`
	Timestamp int64                  `json:"timestamp"`
	ExtraData map[string]interface{} `json:"extra_data,omitempty"`
}

type PermissionResponse struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

const permissionTimeout = 180 * time.Second

// ---------------------------------------------------------------------------
// BridgeServer — pure HTTP, no Telegram dependency
// ---------------------------------------------------------------------------

type BridgeServer struct {
	logger  *slog.Logger
	port    int
	pending map[string]pendingEntry
	mu      sync.Mutex
}

type pendingEntry struct {
	request PermissionRequest
	ch      chan PermissionResponse
}

func NewBridgeServer(port int) *BridgeServer {
	return &BridgeServer{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		port:    port,
		pending: make(map[string]pendingEntry),
	}
}

func (bs *BridgeServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/request-permission", bs.handlePermissionRequest)
	mux.HandleFunc("/respond", bs.handleResponse)
	mux.HandleFunc("/pending", bs.handlePending)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	addr := fmt.Sprintf("127.0.0.1:%d", bs.port)
	bs.logger.Info("Bridge listening", "addr", addr)
	return http.ListenAndServe(addr, mux)
}

// ---------------------------------------------------------------------------
// POST /request-permission — wallet pushes here, blocks until decision
// ---------------------------------------------------------------------------

func (bs *BridgeServer) handlePermissionRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	bs.logger.Info("Permission request", "id", req.ID, "type", req.Type,
		"app", req.App, "amount", req.Amount)

	ch := make(chan PermissionResponse, 1)
	bs.mu.Lock()
	bs.pending[req.ID] = pendingEntry{request: req, ch: ch}
	bs.mu.Unlock()

	select {
	case resp := <-ch:
		bs.mu.Lock()
		delete(bs.pending, req.ID)
		bs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)

	case <-time.After(permissionTimeout):
		bs.mu.Lock()
		delete(bs.pending, req.ID)
		bs.mu.Unlock()
		w.WriteHeader(http.StatusGatewayTimeout)
		fmt.Fprintf(w, `{"error":"timeout","id":"%s"}`, req.ID)
	}
}

// ---------------------------------------------------------------------------
// POST /respond — external agent (OpenClaw) sends user's decision here
// ---------------------------------------------------------------------------

func (bs *BridgeServer) handleResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var resp PermissionResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	bs.mu.Lock()
	entry, ok := bs.pending[resp.ID]
	bs.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"unknown id"}`))
		return
	}

	entry.ch <- resp
	bs.logger.Info("Decision received", "id", resp.ID, "approved", resp.Approved)

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---------------------------------------------------------------------------
// GET /pending — returns all pending permission requests (for polling agents)
// ---------------------------------------------------------------------------

func (bs *BridgeServer) handlePending(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bs.mu.Lock()
	requests := make([]PermissionRequest, 0, len(bs.pending))
	for _, entry := range bs.pending {
		requests = append(requests, entry.request)
	}
	bs.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pending": requests,
		"count":   len(requests),
	})
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	bridgePort := flag.Int("port", 18790, "Bridge server port")
	flag.Parse()

	bridge := NewBridgeServer(*bridgePort)

	go func() {
		if err := bridge.Start(); err != nil {
			log.Fatalf("Bridge server error: %v", err)
		}
	}()

	bridge.logger.Info("Gebunden Bridge started", "port", *bridgePort)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	bridge.logger.Info("Bridge shutdown")
}
