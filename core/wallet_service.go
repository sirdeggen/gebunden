package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	sdktx "github.com/bsv-blockchain/go-sdk/transaction"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/defs"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/monitor"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/services"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/storage"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wallet"
	"github.com/bsv-blockchain/go-wallet-toolbox/pkg/wdk"
)

// WalletService manages wallet lifecycle and provides Wails-bound methods
type WalletService struct {
	mu             sync.RWMutex
	wallet         *wallet.Wallet
	storage        *storage.Provider
	monitor        *monitor.Daemon
	services       *services.WalletServices
	logger         *slog.Logger
	chain          defs.BSVNetwork
	ctx            context.Context
	cancel         context.CancelFunc
	permissionGate PermissionGate
}

// NewWalletService creates a new WalletService
func NewWalletService() *WalletService {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	return &WalletService{
		logger: logger,
		chain:  defs.NetworkMainnet,
	}
}

// InitializeWallet creates and initializes the wallet with the given private key and chain
func (ws *WalletService) InitializeWallet(privateKeyHex string, chain string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.wallet != nil {
		ws.logger.Info("Wallet already initialized, skipping")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	ws.ctx = ctx
	ws.cancel = cancel

	network, err := defs.ParseBSVNetworkStr(chain)
	if err != nil {
		cancel()
		return fmt.Errorf("invalid network: %w", err)
	}
	ws.chain = network

	ws.logger.Info("Initializing wallet", "chain", chain)

	// Create services
	activeServices := services.New(ws.logger, defs.DefaultServicesConfig(network))
	ws.services = activeServices

	// Determine database path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		cancel()
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	dataDir := filepath.Join(homeDir, ".gebunden")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		cancel()
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	identityKey, err := wdk.IdentityKey(privateKeyHex)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to derive identity key: %w", err)
	}

	dbPath := filepath.Join(dataDir, fmt.Sprintf("wallet-%s-%s.sqlite", identityKey, chain))

	// Create GORM storage provider with SQLite
	dbConfig := defs.DefaultDBConfig()
	dbConfig.Engine = defs.DBTypeSQLite
	dbConfig.SQLite.ConnectionString = dbPath

	providerOpts := []storage.ProviderOption{
		storage.WithDBConfig(dbConfig),
		storage.WithFeeModel(defs.FeeModel{Type: defs.SatPerKB, Value: 100}),
		storage.WithCommission(defs.DefaultCommission()),
		storage.WithLogger(ws.logger),
		storage.WithBackgroundBroadcasterContext(ctx),
	}

	activeStorage, err := storage.NewGORMProvider(network, activeServices, providerOpts...)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create storage provider: %w", err)
	}
	ws.storage = activeStorage

	// Run migrations
	_, err = activeStorage.Migrate(ctx, "BSV Desktop Wallet", identityKey)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to migrate storage: %w", err)
	}

	// Create wallet
	w, err := wallet.New(network, privateKeyHex, activeStorage,
		wallet.WithLogger(ws.logger),
		wallet.WithServices(activeServices),
	)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create wallet: %w", err)
	}
	ws.wallet = w

	// Start monitor daemon
	daemon, err := monitor.NewDaemonWithGORMLocker(ctx, ws.logger, activeStorage, activeStorage.Database.DB)
	if err != nil {
		ws.logger.Warn("Failed to create monitor daemon", "error", err)
	} else {
		ws.monitor = daemon
		monitorConfig := defs.DefaultMonitorConfig()
		if err := daemon.Start(monitorConfig.Tasks.EnabledTasks()); err != nil {
			ws.logger.Warn("Failed to start monitor", "error", err)
		} else {
			ws.logger.Info("Monitor daemon started")
		}
	}

	ws.logger.Info("Wallet initialized successfully", "chain", chain)
	return nil
}

// ShutdownWallet gracefully shuts down the wallet
func (ws *WalletService) ShutdownWallet() error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if ws.monitor != nil {
		_ = ws.monitor.Stop()
		ws.monitor = nil
	}

	if ws.wallet != nil {
		ws.wallet.Close()
		ws.wallet = nil
	}

	if ws.cancel != nil {
		ws.cancel()
	}

	ws.logger.Info("Wallet shut down")
	return nil
}

// IsWalletReady returns whether the wallet is initialized
func (ws *WalletService) IsWalletReady() bool {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.wallet != nil
}

// GetNetwork returns the current network
func (ws *WalletService) GetNetwork() string {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return string(ws.chain)
}

// GetSettings returns the user settings JSON from disk
func (ws *WalletService) GetSettings() (string, error) {
	settingsPath, err := ws.settingsPath()
	if err != nil {
		return "{}", nil
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return "{}", nil // No settings file yet, return empty object
	}
	return string(data), nil
}

// SetSettings saves the user settings JSON to disk
func (ws *WalletService) SetSettings(settingsJSON string) error {
	settingsPath, err := ws.settingsPath()
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, []byte(settingsJSON), 0o644)
}

func (ws *WalletService) settingsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	dataDir := filepath.Join(homeDir, ".gebunden")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create data directory: %w", err)
	}
	return filepath.Join(dataDir, "settings.json"), nil
}

// SetPermissionGate sets the permission gate for user approval flows.
func (ws *WalletService) SetPermissionGate(gate PermissionGate) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.permissionGate = gate
}

// checkPermission sends a typed PermissionRequest to the gate and returns an error if denied.
func checkPermission(gate PermissionGate, method, origin string, permType string, extra map[string]interface{}, amount int64, message string) error {
	if gate == nil {
		return nil
	}
	reqID := fmt.Sprintf("%s-%s-%d", method, origin, time.Now().UnixNano())
	if message == "" {
		message = fmt.Sprintf("%s requested by %s", method, origin)
	}
	approved, err := gate.RequestPermission(PermissionRequest{
		ID:        reqID,
		Type:      permType,
		App:       origin,
		Origin:    origin,
		Message:   message,
		Amount:    amount,
		Timestamp: time.Now().Unix(),
		ExtraData: extra,
	})
	if err != nil {
		return fmt.Errorf("permission error: %w", err)
	}
	if !approved {
		return fmt.Errorf("permission denied by user for %s from %s", method, origin)
	}
	return nil
}

// --- BRC-100 Wallet Interface Methods ---
// CallWalletMethod dispatches a wallet method call by name with JSON args and origin.
// This is the single entry point for both the HTTP server and frontend calls.
func (ws *WalletService) CallWalletMethod(method string, argsJSON string, origin string) (string, error) {
	ws.mu.RLock()
	w := ws.wallet
	gate := ws.permissionGate
	ws.mu.RUnlock()

	if w == nil {
		return "", fmt.Errorf("wallet not initialized")
	}

	ctx := context.Background()
	var result any
	var err error

	switch method {

	// ---------------------------------------------------------------
	// Spend Authorization — createAction
	// ---------------------------------------------------------------
	case "createAction":
		var args SDKCreateActionArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		// Calculate total output satoshis for the spend prompt
		var totalSats uint64
		for _, o := range args.Outputs {
			totalSats += o.Satoshis
		}
		if args.InputBEEF != nil {
			if beef, e := sdktx.NewBeefFromBytes(args.InputBEEF); e == nil {
				for i := range args.Inputs {
					op := args.Inputs[i].Outpoint
					beefTx := beef.Transactions[op.Txid]
					if beefTx == nil || beefTx.Transaction == nil {
						continue
					}
					if int(op.Index) >= len(beefTx.Transaction.Outputs) {
						continue
					}
					totalSats -= beefTx.Transaction.Outputs[op.Index].Satoshis
				}
			}
		}
		if int64(totalSats) > 0 {
			extra := map[string]interface{}{
				"description": args.Description,
				"outputCount": len(args.Outputs),
				"inputCount":  len(args.Inputs),
			}
			if len(args.Labels) > 0 {
				extra["labels"] = args.Labels
			}
			if err := checkPermission(gate, method, origin, "spend", extra, int64(totalSats),
				fmt.Sprintf("Create transaction: %s (%d sats)", args.Description, totalSats)); err != nil {
				return "", err
			}
		}
		result, err = w.CreateAction(ctx, args, origin)

	// ---------------------------------------------------------------
	// Spend Authorization — signAction
	// ---------------------------------------------------------------
	case "signAction":
		var args SDKSignActionArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.SignAction(ctx, args, origin)

	case "abortAction":
		var args SDKAbortActionArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.AbortAction(ctx, args, origin)

	case "listActions":
		var args SDKListActionsArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.ListActions(ctx, args, origin)

	// ---------------------------------------------------------------
	// Spend Authorization — internalizeAction
	// ---------------------------------------------------------------
	case "internalizeAction":
		var args SDKInternalizeActionArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.InternalizeAction(ctx, args, origin)

	// ---------------------------------------------------------------
	// Basket Access — listOutputs
	// ---------------------------------------------------------------
	case "listOutputs":
		var args SDKListOutputsArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.ListOutputs(ctx, args, origin)

	case "relinquishOutput":
		var args SDKRelinquishOutputArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.RelinquishOutput(ctx, args, origin)

	// ---------------------------------------------------------------
	// Protocol Access — getPublicKey, encrypt, decrypt, hmac, signatures
	// ---------------------------------------------------------------
	case "getPublicKey":
		var args SDKGetPublicKeyArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.GetPublicKey(ctx, args, origin)

	case "encrypt":
		var args SDKEncryptArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.Encrypt(ctx, args, origin)

	case "decrypt":
		var args SDKDecryptArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		extra := map[string]interface{}{
			"protocolID":    args.ProtocolID.Protocol,
			"securityLevel": args.ProtocolID.SecurityLevel,
			"keyID":         args.KeyID,
		}
		if err := checkPermission(gate, method, origin, "protocol", extra, 0,
			fmt.Sprintf("Decrypt data using protocol: %s", args.ProtocolID.Protocol)); err != nil {
			return "", err
		}
		result, err = w.Decrypt(ctx, args, origin)

	case "createHmac":
		var args SDKCreateHMACArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.CreateHMAC(ctx, args, origin)

	case "verifyHmac":
		var args SDKVerifyHMACArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.VerifyHMAC(ctx, args, origin)

	case "createSignature":
		var args SDKCreateSignatureArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.CreateSignature(ctx, args, origin)

	case "verifySignature":
		var args SDKVerifySignatureArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.VerifySignature(ctx, args, origin)

	// ---------------------------------------------------------------
	// Counterparty — revealCounterpartyKeyLinkage, revealSpecificKeyLinkage
	// ---------------------------------------------------------------
	case "revealCounterpartyKeyLinkage":
		var args SDKRevealCounterpartyKeyLinkageArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		extra := map[string]interface{}{}
		if args.Counterparty != nil {
			extra["counterparty"] = args.Counterparty.ToDERHex()
		}
		if args.Verifier != nil {
			extra["verifier"] = args.Verifier.ToDERHex()
		}
		if err := checkPermission(gate, method, origin, "counterparty", extra, 0,
			"Reveal counterparty key linkage"); err != nil {
			return "", err
		}
		result, err = w.RevealCounterpartyKeyLinkage(ctx, args, origin)

	case "revealSpecificKeyLinkage":
		var args SDKRevealSpecificKeyLinkageArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		extra := map[string]interface{}{
			"protocolID": args.ProtocolID.Protocol,
			"keyID":      args.KeyID,
		}
		if args.Verifier != nil {
			extra["verifier"] = args.Verifier.ToDERHex()
		}
		if err := checkPermission(gate, method, origin, "counterparty", extra, 0,
			fmt.Sprintf("Reveal specific key linkage for protocol: %s", args.ProtocolID.Protocol)); err != nil {
			return "", err
		}
		result, err = w.RevealSpecificKeyLinkage(ctx, args, origin)

	// ---------------------------------------------------------------
	// Certificate Access — acquire, prove, relinquish
	// ---------------------------------------------------------------
	case "acquireCertificate":
		var args SDKAcquireCertificateArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.AcquireCertificate(ctx, args, origin)

	case "listCertificates":
		var args SDKListCertificatesArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.ListCertificates(ctx, args, origin)

	case "proveCertificate":
		var args SDKProveCertificateArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		extra := map[string]interface{}{
			"certificateType": args.Certificate.Type.String(),
			"fieldsToReveal":  args.FieldsToReveal,
		}
		if args.Verifier != nil {
			extra["verifierPublicKey"] = args.Verifier.ToDERHex()
		}
		if err := checkPermission(gate, method, origin, "certificate", extra, 0,
			fmt.Sprintf("Prove certificate (type: %s) to verifier", args.Certificate.Type.String())); err != nil {
			return "", err
		}
		result, err = w.ProveCertificate(ctx, args, origin)

	case "relinquishCertificate":
		var args SDKRelinquishCertificateArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		extra := map[string]interface{}{
			"certificateType": args.Type.String(),
		}
		if err := checkPermission(gate, method, origin, "certificate", extra, 0,
			fmt.Sprintf("Relinquish certificate of type: %s", args.Type.String())); err != nil {
			return "", err
		}
		result, err = w.RelinquishCertificate(ctx, args, origin)

	case "discoverByIdentityKey":
		var args SDKDiscoverByIdentityKeyArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.DiscoverByIdentityKey(ctx, args, origin)

	case "discoverByAttributes":
		var args SDKDiscoverByAttributesArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.DiscoverByAttributes(ctx, args, origin)

	case "isAuthenticated":
		result, err = w.IsAuthenticated(ctx, nil, origin)

	case "waitForAuthentication":
		result, err = w.WaitForAuthentication(ctx, nil, origin)

	case "getHeight":
		result, err = w.GetHeight(ctx, nil, origin)

	case "getHeaderForHeight":
		var args SDKGetHeaderArgs
		if e := json.Unmarshal([]byte(argsJSON), &args); e != nil {
			return "", fmt.Errorf("invalid args: %w", e)
		}
		result, err = w.GetHeaderForHeight(ctx, args, origin)

	case "getNetwork":
		result, err = w.GetNetwork(ctx, nil, origin)

	case "getVersion":
		result, err = w.GetVersion(ctx, nil, origin)

	default:
		return "", fmt.Errorf("unknown wallet method: %s", method)
	}

	if err != nil {
		return "", err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}

	return string(resultJSON), nil
}
