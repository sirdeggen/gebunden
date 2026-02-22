# Gebunden

Headless BRC-100 wallet service with an OpenClaw text-based bridge.

## Overview

Gebunden is a headless fork of the BSV desktop wallet, designed to run as a background service ("daemon") that exposes the standard BRC-100 `WalletInterface` over localhost HTTP.

Instead of a desktop GUI window, Gebunden uses a separate **Bridge Service** to interact with the user via their preferred chat channel (currently Telegram). When an application requests a sensitive action (like spending funds or accessing a protocol), Gebunden suspends the request and pushes a prompt to the user's chat. The user approves or denies with a button click, and the wallet resumes or rejects the action accordingly.

## Architecture

This repository is a monorepo containing two components:

- **`gebunden-src/`**: The headless wallet daemon.
  - Exposes the BRC-100 HTTP interface on `http://127.0.0.1:3321`.
  - Manages private keys and UTXOs locally (SQLite).
  - Delegates permission requests to the Bridge.

- **`bridge/`**: The permission bridge service.
  - Exposes an internal API on `http://127.0.0.1:18790`.
  - Connects to the user's chat provider (Telegram).
  - Converts wallet permission requests into interactive chat prompts.

## Configuration

### Wallet Identity & Storage

Gebunden stores its data in **`~/.gebunden`** (macOS/Linux).
- **Wallet DB**: `~/.gebunden/wallet-<identityKey>-mainnet.sqlite`
- **Settings**: `~/.gebunden/settings.json`

To run, it needs a `wallet-identity.json` file containing your root key. It searches in this order:
1. Path specified by `-key-file` flag
2. `GEBUNDEN_PRIVATE_KEY` environment variable (root key hex)
3. `~/.gebunden/wallet-identity.json`
4. `~/.clawdbot/bsv-wallet/wallet-identity.json` (legacy fallback)

### Telegram Bridge

The bridge needs a Telegram Bot Token and your Chat ID. It discovers them automatically from your OpenClaw config or environment variables.

**Priority Order:**
1. **Command Line Flags**: `-telegram-token` and `-telegram-chat`
2. **Environment Variables**: `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`
3. **OpenClaw Config**: `~/.openclaw/openclaw.json` (looks for `channels.telegram.botToken`)

**Security Note:** Do not commit secrets to this repository. Use the OpenClaw config or environment variables.

## Usage

### 1. Build

```bash
# Build the bridge
cd bridge
go build -o ../bin/bridge

# Build the wallet daemon
cd ../gebunden-src
go build -tags headless -o ../bin/gebunden
```

### 2. Run

Start the bridge first, then the wallet.

```bash
# Terminal 1: Start Bridge
./bin/bridge

# Terminal 2: Start Wallet (Headless)
./bin/gebunden --headless
```

### 3. Usage

Applications on your machine can now use the wallet via the standard HTTP interface:

```bash
curl -X POST http://127.0.0.1:3321/getPublicKey \
  -H "Content-Type: application/json" \
  -d '{"protocolID":{"protocol":"identity","securityLevel":1}}'
```

If the action requires permission, you will receive a message on Telegram. Click "Approve" to let the request proceed. The default timeout for prompts is **180 seconds**.

## Permission Types

The bridge supports rich prompts for:
- **Spending**: Amount, recipient, description.
- **Protocol Access**: Protocol ID, counterparty, security level.
- **Certificates**: Acquisition, proving, listing certificates.
- **Baskets**: Token basket access.
- **Counterparty**: Key linkage and identity verification.

## Development

- **Language**: Go 1.22+
- **Data**: SQLite (CGO required)

## License

See LICENSE file.
