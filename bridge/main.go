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

// PermissionRequest is pushed by the wallet when user approval is needed.
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

// PermissionResponse is returned to the wallet after the user decides.
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
	pending       map[string]chan PermissionResponse
	mu            sync.Mutex
	stopCh        chan struct{}
}

func NewBridgeServer(port int, telegramToken, telegramChat string) *BridgeServer {
	return &BridgeServer{
		logger: slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
		port:          port,
		telegramToken: telegramToken,
		telegramChat:  telegramChat,
		pending:       make(map[string]chan PermissionResponse),
		stopCh:        make(chan struct{}),
	}
}

func (bs *BridgeServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/request-permission", bs.handlePermissionRequest)
	mux.HandleFunc("/respond", bs.handleResponse)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
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
// HTTP: wallet pushes permission request, blocks until decision
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
	bs.pending[req.ID] = ch
	bs.mu.Unlock()

	go bs.sendToTelegram(req)

	select {
	case resp := <-ch:
		bs.cleanup(req.ID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	case <-time.After(permissionTimeout):
		bs.cleanup(req.ID)
		w.WriteHeader(http.StatusGatewayTimeout)
		fmt.Fprintf(w, `{"error":"timeout","id":"%s"}`, req.ID)
	}
}

// handleResponse allows manual / programmatic decisions (e.g. from OpenClaw agent).
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
	ch, ok := bs.pending[resp.ID]
	bs.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":"unknown id"}`, http.StatusNotFound)
		return
	}
	ch <- resp
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (bs *BridgeServer) cleanup(id string) {
	bs.mu.Lock()
	delete(bs.pending, id)
	bs.mu.Unlock()
}

func (bs *BridgeServer) resolve(id string, approved bool, reason string) {
	bs.mu.Lock()
	ch, ok := bs.pending[id]
	bs.mu.Unlock()
	if ok {
		ch <- PermissionResponse{ID: id, Approved: approved, Reason: reason}
	}
}

// ---------------------------------------------------------------------------
// Telegram: send prompt with type-specific formatting
// ---------------------------------------------------------------------------

func (bs *BridgeServer) sendToTelegram(req PermissionRequest) {
	if bs.telegramToken == "" || bs.telegramChat == "" {
		bs.logger.Warn("Telegram not configured, auto-approving", "id", req.ID)
		bs.resolve(req.ID, true, "auto-approved (no telegram)")
		return
	}

	text := bs.formatPrompt(req)
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
		bs.resolve(req.ID, false, "telegram send failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		bs.logger.Error("Telegram API error", "status", resp.StatusCode, "body", string(body))
		bs.resolve(req.ID, false, "telegram api error")
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

func (bs *BridgeServer) formatPrompt(req PermissionRequest) string {
	var b strings.Builder

	switch req.Type {
	case "spend":
		b.WriteString("💸 <b>Spending Authorization</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if req.Amount > 0 {
			b.WriteString(fmt.Sprintf("<b>Amount:</b> %d sats\n", req.Amount))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Description:</b> %s\n", htmlEsc(req.Message)))
		}

	case "protocol":
		b.WriteString("🔗 <b>Protocol Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if pid, ok := req.ExtraData["protocolID"]; ok {
			b.WriteString(fmt.Sprintf("<b>Protocol:</b> %v\n", pid))
		}
		if sl, ok := req.ExtraData["securityLevel"]; ok {
			b.WriteString(fmt.Sprintf("<b>Security Level:</b> %v\n", sl))
		}
		if cp, ok := req.ExtraData["counterparty"]; ok {
			b.WriteString(fmt.Sprintf("<b>Counterparty:</b> <code>%s</code>\n", htmlEsc(fmt.Sprint(cp))))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Reason:</b> %s\n", htmlEsc(req.Message)))
		}

	case "basket":
		b.WriteString("🧺 <b>Basket Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if basket, ok := req.ExtraData["basket"]; ok {
			b.WriteString(fmt.Sprintf("<b>Basket:</b> %v\n", basket))
		}
		if reason, ok := req.ExtraData["reason"]; ok {
			b.WriteString(fmt.Sprintf("<b>Reason:</b> %s\n", htmlEsc(fmt.Sprint(reason))))
		}
		if renewal, ok := req.ExtraData["renewal"]; ok && renewal == true {
			b.WriteString("<i>(renewal)</i>\n")
		}

	case "certificate":
		b.WriteString("📜 <b>Certificate Access Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if ct, ok := req.ExtraData["certificateType"]; ok {
			b.WriteString(fmt.Sprintf("<b>Type:</b> %v\n", ct))
		}
		if vpk, ok := req.ExtraData["verifierPublicKey"]; ok {
			b.WriteString(fmt.Sprintf("<b>Verifier:</b> <code>%s</code>\n", htmlEsc(fmt.Sprint(vpk))))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Reason:</b> %s\n", htmlEsc(req.Message)))
		}
		if renewal, ok := req.ExtraData["renewal"]; ok && renewal == true {
			b.WriteString("<i>(renewal)</i>\n")
		}

	case "group":
		b.WriteString("📋 <b>Grouped Permission Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("%s\n", htmlEsc(req.Message)))
		}
		if spend, ok := req.ExtraData["spendingAmount"]; ok {
			b.WriteString(fmt.Sprintf("• Spending: %v sats\n", spend))
		}
		if protos, ok := req.ExtraData["protocolCount"]; ok {
			b.WriteString(fmt.Sprintf("• Protocols: %v\n", protos))
		}
		if baskets, ok := req.ExtraData["basketCount"]; ok {
			b.WriteString(fmt.Sprintf("• Baskets: %v\n", baskets))
		}
		if certs, ok := req.ExtraData["certificateCount"]; ok {
			b.WriteString(fmt.Sprintf("• Certificates: %v\n", certs))
		}

	case "counterparty":
		b.WriteString("🤝 <b>Counterparty Permission Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		if cp, ok := req.ExtraData["counterparty"]; ok {
			b.WriteString(fmt.Sprintf("<b>Counterparty:</b> <code>%s</code>\n", htmlEsc(fmt.Sprint(cp))))
		}
		if protos, ok := req.ExtraData["protocols"]; ok {
			b.WriteString(fmt.Sprintf("<b>Protocols:</b> %v\n", protos))
		}
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Details:</b> %s\n", htmlEsc(req.Message)))
		}

	default:
		b.WriteString("🔐 <b>Permission Request</b>\n\n")
		b.WriteString(fmt.Sprintf("<b>App:</b> <code>%s</code>\n", htmlEsc(req.App)))
		b.WriteString(fmt.Sprintf("<b>Type:</b> %s\n", htmlEsc(req.Type)))
		if req.Message != "" {
			b.WriteString(fmt.Sprintf("<b>Details:</b> %s\n", htmlEsc(req.Message)))
		}
	}

	b.WriteString(fmt.Sprintf("\n<code>%s</code>", htmlEsc(req.ID)))
	return b.String()
}

// htmlEsc escapes HTML special characters for Telegram HTML parse mode.
func htmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
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
				// Preserve original text, append result, remove buttons
				newText := cq.Message.Text + "\n\n" + resultLabel
				bs.editMessage(baseURL, cq.Message.Chat.ID, cq.Message.MessageID, newText)
			}
		}
	}
}

func (bs *BridgeServer) answerCallback(baseURL, callbackID string, approved bool) {
	text := "✅ Approved"
	if !approved {
		text = "❌ Denied"
	}
	payload := map[string]interface{}{
		"callback_query_id": callbackID,
		"text":              text,
	}
	body, _ := json.Marshal(payload)
	http.Post(baseURL+"/answerCallbackQuery", "application/json", bytes.NewBuffer(body))
}

func (bs *BridgeServer) editMessage(baseURL string, chatID int64, messageID int, newText string) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       newText,
	}
	body, _ := json.Marshal(payload)
	http.Post(baseURL+"/editMessageText", "application/json", bytes.NewBuffer(body))
}

// ---------------------------------------------------------------------------
// Config: read Telegram bot token from OpenClaw config
// Path: ~/.openclaw/openclaw.json → channels.telegram.botToken
// ---------------------------------------------------------------------------

func readOpenClawConfig() (token string) {
	// Env override takes priority
	if t := os.Getenv("TELEGRAM_BOT_TOKEN"); t != "" {
		return t
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	configPath := filepath.Join(home, ".openclaw", "openclaw.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}

	var cfg struct {
		Channels struct {
			Telegram struct {
				BotToken string `json:"botToken"`
			} `json:"telegram"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.Channels.Telegram.BotToken
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	bridgePort := flag.Int("port", 18790, "Bridge server port")
	flagToken := flag.String("telegram-token", "", "Telegram Bot Token (overrides OpenClaw config)")
	flagChat := flag.String("telegram-chat", os.Getenv("TELEGRAM_CHAT_ID"), "Telegram chat ID for prompts")
	flag.Parse()

	// Token: flag > env > openclaw config
	telegramToken := *flagToken
	if telegramToken == "" {
		telegramToken = readOpenClawConfig()
	}

	bridge := NewBridgeServer(*bridgePort, telegramToken, *flagChat)

	go func() {
		if err := bridge.Start(); err != nil {
			log.Fatalf("Bridge server error: %v", err)
		}
	}()

	bridge.logger.Info("Gebunden Bridge started",
		"port", *bridgePort,
		"telegram_configured", telegramToken != "",
		"chat_configured", *flagChat != "",
	)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	bridge.Stop()
	bridge.logger.Info("Bridge shutdown")
}
