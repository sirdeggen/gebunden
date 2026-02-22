package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type PermissionRequest struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
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
// BridgeServer
// ---------------------------------------------------------------------------

type BridgeServer struct {
	logger        *slog.Logger
	port          int
	telegramToken string
	telegramChat  string
	pending       map[string]pendingEntry
	mu            sync.Mutex
	stopCh        chan struct{}
}

type pendingEntry struct {
	request PermissionRequest
	ch      chan PermissionResponse
}

func NewBridgeServer(port int, telegramToken, telegramChat string) *BridgeServer {
	return &BridgeServer{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		port:          port,
		telegramToken: telegramToken,
		telegramChat:  telegramChat,
		pending:       make(map[string]pendingEntry),
		stopCh:        make(chan struct{}),
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

	if bs.telegramToken != "" {
		go bs.pollTelegramUpdates()
	}

	addr := fmt.Sprintf("127.0.0.1:%d", bs.port)
	bs.logger.Info("Bridge listening", "addr", addr)
	return http.ListenAndServe(addr, mux)
}

func (bs *BridgeServer) Stop() { close(bs.stopCh) }

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

	// Send Telegram prompt if configured
	go bs.sendToTelegram(req)

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
// POST /respond — external decision (fallback for non-Telegram setups)
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
	bs.resolve(resp.ID, resp.Approved, resp.Reason)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// ---------------------------------------------------------------------------
// GET /pending — for polling agents
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
	json.NewEncoder(w).Encode(map[string]interface{}{"pending": requests, "count": len(requests)})
}

// ---------------------------------------------------------------------------
// Telegram: send prompt with inline buttons
// ---------------------------------------------------------------------------

func (bs *BridgeServer) sendToTelegram(req PermissionRequest) {
	if bs.telegramToken == "" || bs.telegramChat == "" {
		return
	}

	text := formatPrompt(req)
	approveLabel := promptButton(req.Type)
	keyboard := [][]map[string]interface{}{
		{
			{"text": approveLabel, "callback_data": fmt.Sprintf("approve:%s", req.ID)},
			{"text": "❌ Deny", "callback_data": fmt.Sprintf("deny:%s", req.ID)},
		},
	}

	payload := map[string]interface{}{
		"chat_id":      bs.telegramChat,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": keyboard},
	}
	payloadJSON, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", bs.telegramToken)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(payloadJSON))
	if err != nil {
		bs.logger.Error("Telegram send failed", "error", err, "id", req.ID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bs.logger.Error("Telegram API error", "status", resp.StatusCode, "body", string(body))
		return
	}
	bs.logger.Info("Prompt sent to Telegram", "id", req.ID, "type", req.Type)
}

func promptButton(permType string) string {
	switch permType {
	case "spend":
		return "💸 Send"
	case "protocol":
		return "🔗 Grant Access"
	case "basket":
		return "🧺 Grant Access"
	case "certificate":
		return "📜 Grant Access"
	case "group":
		return "✅ Grant Selected"
	case "counterparty":
		return "🤝 Allow"
	default:
		return "✅ Approve"
	}
}

func formatPrompt(req PermissionRequest) string {
	var b strings.Builder

	switch req.Type {
	case "spend":
		b.WriteString("💸 <b>Spending Authorization</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if req.Amount > 0 {
			b.WriteString(fmt.Sprintf("<b>Amount:</b> %d sats\n", req.Amount))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Description:</b> %s\n", h(req.Message)))
		}

	case "protocol":
		b.WriteString("🔗 <b>Protocol Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if pid, ok := req.ExtraData["protocolID"]; ok {
			b.WriteString(fmt.Sprintf("<b>Protocol:</b> %v\n", pid))
		}
		if sl, ok := req.ExtraData["securityLevel"]; ok {
			b.WriteString(fmt.Sprintf("<b>Security Level:</b> %v\n", sl))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Reason:</b> %s\n", h(req.Message)))
		}

	case "basket":
		b.WriteString("🧺 <b>Basket Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if basket, ok := req.ExtraData["basket"]; ok {
			b.WriteString(fmt.Sprintf("<b>Basket:</b> %v\n", basket))
		}

	case "certificate":
		b.WriteString("📜 <b>Certificate Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if ct, ok := req.ExtraData["certificateType"]; ok {
			b.WriteString(fmt.Sprintf("<b>Type:</b> %v\n", ct))
		}
		if vpk, ok := req.ExtraData["verifierPublicKey"]; ok {
			b.WriteString(fmt.Sprintf("<b>Verifier:</b> <code>%s</code>\n", h(fmt.Sprint(vpk))))
		}

	case "group":
		b.WriteString("📋 <b>Grouped Permission Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if spend, ok := req.ExtraData["spendingAmount"]; ok {
			b.WriteString(fmt.Sprintf("• Spending: %v sats\n", spend))
		}
		if protos, ok := req.ExtraData["protocolCount"]; ok {
			b.WriteString(fmt.Sprintf("• Protocols: %v\n", protos))
		}

	case "counterparty":
		b.WriteString("🤝 <b>Counterparty Permission</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		if cp, ok := req.ExtraData["counterparty"]; ok {
			b.WriteString(fmt.Sprintf("<b>Counterparty:</b> <code>%s</code>\n", h(fmt.Sprint(cp))))
		}

	default:
		b.WriteString("🔐 <b>Permission Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", h(req.App)))
		b.WriteString(fmt.Sprintf("<b>Type:</b> %s\n", h(req.Type)))
	}

	if req.Message != "" && req.Type != "spend" && req.Type != "protocol" {
		b.WriteString(fmt.Sprintf("<b>Details:</b> %s\n", h(req.Message)))
	}
	return b.String()
}

// h escapes HTML entities for Telegram.
func h(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func (bs *BridgeServer) resolve(id string, approved bool, reason string) {
	bs.mu.Lock()
	entry, ok := bs.pending[id]
	bs.mu.Unlock()
	if ok {
		entry.ch <- PermissionResponse{ID: id, Approved: approved, Reason: reason}
	}
}

// ---------------------------------------------------------------------------
// Telegram: long-poll for callback_query (button clicks)
// ---------------------------------------------------------------------------

func (bs *BridgeServer) pollTelegramUpdates() {
	offset := 0
	baseURL := fmt.Sprintf("https://api.telegram.org/bot%s", bs.telegramToken)

	for {
		select {
		case <-bs.stopCh:
			return
		default:
		}

		payload := map[string]interface{}{
			"offset":          offset,
			"timeout":         30,
			"allowed_updates": []string{"callback_query"},
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(baseURL+"/getUpdates", "application/json", bytes.NewBuffer(body))
		if err != nil {
			bs.logger.Error("Telegram poll error", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		var result struct {
			OK     bool `json:"ok"`
			Result []struct {
				UpdateID      int `json:"update_id"`
				CallbackQuery *struct {
					ID      string `json:"id"`
					Data    string `json:"data"`
					Message *struct {
						MessageID int `json:"message_id"`
						Chat      struct {
							ID int64 `json:"id"`
						} `json:"chat"`
						Text string `json:"text"`
					} `json:"message"`
				} `json:"callback_query"`
			} `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()

		for _, u := range result.Result {
			offset = u.UpdateID + 1
			cq := u.CallbackQuery
			if cq == nil || cq.Data == "" {
				continue
			}

			parts := strings.SplitN(cq.Data, ":", 2)
			if len(parts) != 2 {
				continue
			}
			action, reqID := parts[0], parts[1]
			approved := action == "approve"

			bs.logger.Info("Telegram callback", "action", action, "reqID", reqID)
			bs.resolve(reqID, approved, "user via telegram")
			bs.answerCallback(baseURL, cq.ID, approved)

			if cq.Message != nil {
				resultLabel := "✅ Approved"
				if !approved {
					resultLabel = "❌ Denied"
				}
				bs.editMessage(baseURL, cq.Message.Chat.ID, cq.Message.MessageID,
					cq.Message.Text+"\n\n"+resultLabel)
			}
		}
	}
}

func (bs *BridgeServer) answerCallback(baseURL, callbackID string, approved bool) {
	text := "✅ Approved"
	if !approved {
		text = "❌ Denied"
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"callback_query_id": callbackID,
		"text":              text,
	})
	http.Post(baseURL+"/answerCallbackQuery", "application/json", bytes.NewBuffer(payload))
}

func (bs *BridgeServer) editMessage(baseURL string, chatID int64, messageID int, newText string) {
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       newText,
	})
	http.Post(baseURL+"/editMessageText", "application/json", bytes.NewBuffer(payload))
}

// ---------------------------------------------------------------------------
// Config: read from ~/.gebunden/bridge-config.json or env
// ---------------------------------------------------------------------------

func readBridgeConfig() (token, chatID string) {
	token = os.Getenv("GEBUNDEN_BOT_TOKEN")
	chatID = os.Getenv("GEBUNDEN_CHAT_ID")
	if token != "" {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".gebunden", "bridge-config.json"))
	if err != nil {
		return
	}
	var cfg struct {
		TelegramBotToken string `json:"telegramBotToken"`
		TelegramChatID   string `json:"telegramChatID"`
	}
	if err := json.Unmarshal(data, &cfg); err == nil {
		if token == "" {
			token = cfg.TelegramBotToken
		}
		if chatID == "" {
			chatID = cfg.TelegramChatID
		}
	}
	return
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	bridgePort := flag.Int("port", 18790, "Bridge server port")
	flagToken := flag.String("telegram-token", "", "Gebunden Telegram Bot Token (overrides config)")
	flagChat := flag.String("telegram-chat", "", "Telegram chat ID for prompts (overrides config)")
	flag.Parse()

	configToken, configChat := readBridgeConfig()
	token := *flagToken
	if token == "" {
		token = configToken
	}
	chat := *flagChat
	if chat == "" {
		chat = configChat
	}

	bridge := NewBridgeServer(*bridgePort, token, chat)

	go func() {
		if err := bridge.Start(); err != nil {
			log.Fatalf("Bridge server error: %v", err)
		}
	}()

	bridge.logger.Info("Gebunden Bridge started",
		"port", *bridgePort,
		"telegram", token != "",
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	bridge.Stop()
	bridge.logger.Info("Bridge shutdown")
}
