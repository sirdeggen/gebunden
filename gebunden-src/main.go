package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

// walletIdentity is the JSON structure for the wallet identity file.
type walletIdentity struct {
	RootKeyHex  string `json:"rootKeyHex"`
	IdentityKey string `json:"identityKey"`
	Network     string `json:"network"`
}

func main() {
	headless := flag.Bool("headless", false, "Run in headless mode (no GUI, HTTP-only)")
	autoApprove := flag.Bool("auto-approve", false, "Auto-approve all permission requests (headless only)")
	keyFile := flag.String("key-file", "", "Path to wallet identity JSON file")
	bridgeURL := flag.String("bridge-url", "http://127.0.0.1:18789", "URL of the Gebunden Bridge service")
	flag.Parse()

	if *headless {
		runHeadless(*autoApprove, *keyFile, *bridgeURL)
	} else {
		runGUI()
	}
}

// runGUI starts the original Wails-based desktop app.
func runGUI() {
	walletService := NewWalletService()
	app := NewApp(walletService)
	nativeService := NewNativeService()
	storageProxyService := NewStorageProxyService()

	err := wails.Run(&options.App{
		Title:  "Gebunden",
		Width:  1200,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnDomReady:       app.domReady,
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Gebunden",
				Message: "Gebunden Wallet\nVersion " + version,
				Icon:    appIcon,
			},
		},
		Bind: []interface{}{
			app,
			walletService,
			nativeService,
			storageProxyService,
		},
	})

	if err != nil {
		log.Fatal("Error:", err.Error())
	}
}

// runHeadless starts the wallet service and HTTP server without the Wails GUI.
func runHeadless(autoApprove bool, keyFile, bridgeURL string) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("Starting Gebunden in headless mode")

	// Load private key
	privateKey, network, err := loadPrivateKey(keyFile)
	if err != nil {
		log.Fatalf("Failed to load private key: %v", err)
	}

	// Initialize wallet
	walletService := NewWalletService()

	// Set up permission gate pointing at the bridge service
	gate := NewBridgePermissionGate(bridgeURL, autoApprove)
	walletService.SetPermissionGate(gate)

	if err := walletService.InitializeWallet(privateKey, network); err != nil {
		log.Fatalf("Failed to initialize wallet: %v", err)
	}
	logger.Info("Wallet initialized", "network", network)

	// Start HTTP server
	httpServer := NewHTTPServer(logger)
	httpServer.SetWalletService(walletService)

	go func() {
		if err := httpServer.Start(walletService.ctx); err != nil {
			logger.Error("HTTP server error", "error", err)
		}
	}()

	logger.Info("Gebunden headless mode running",
		"http", "http://127.0.0.1:3321",
		"bridge", bridgeURL,
		"autoApprove", autoApprove,
	)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	logger.Info("Shutting down...")
	httpServer.Stop()
	walletService.ShutdownWallet()
	logger.Info("Goodbye")
}

// loadPrivateKey loads the wallet private key from a file or environment variable.
// Priority: 1) -key-file flag, 2) GEBUNDEN_PRIVATE_KEY env, 3) ~/.gebunden/wallet-identity.json
func loadPrivateKey(keyFile string) (privateKeyHex, network string, err error) {
	// Check env first
	if envKey := os.Getenv("GEBUNDEN_PRIVATE_KEY"); envKey != "" {
		net := os.Getenv("GEBUNDEN_NETWORK")
		if net == "" {
			net = "mainnet"
		}
		return envKey, net, nil
	}

	// Determine file path
	path := keyFile
	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("failed to get home directory: %w", err)
		}
		// Search paths in order of preference
		candidates := []string{
			filepath.Join(homeDir, ".gebunden", "wallet-identity.json"),
			filepath.Join(homeDir, ".clawdbot", "bsv-wallet", "wallet-identity.json"), // legacy fallback
		}
		for _, c := range candidates {
			if _, statErr := os.Stat(c); statErr == nil {
				path = c
				break
			}
		}
		if path == "" {
			return "", "", fmt.Errorf("no wallet identity file found; tried %v", candidates)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("failed to read key file %s: %w", path, err)
	}

	var identity walletIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return "", "", fmt.Errorf("failed to parse key file: %w", err)
	}

	if identity.RootKeyHex == "" {
		return "", "", fmt.Errorf("rootKeyHex is empty in %s", path)
	}

	net := identity.Network
	if net == "" {
		net = "mainnet"
	}

	return identity.RootKeyHex, net, nil
}
