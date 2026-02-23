# Gebunden

Headless BRC-100 wallet service with an OpenClaw text-based bridge.

## Overview

Gebunden is a headless fork of the BSV desktop wallet, designed to run as a background service ("daemon") that exposes the standard BRC-100 `WalletInterface` over localhost HTTP.

Instead of a desktop GUI window, Gebunden uses a separate **Bridge Service** to interact with the user via their preferred chat channel (currently Telegram). When an application requests a sensitive action (like spending funds or accessing a protocol), Gebunden suspends the request and pushes a prompt to the user's chat. The user approves or denies with a button click, and the wallet resumes or rejects the action accordingly.

## Architecture

This repository is a monorepo containing three components:

- **`core/`**: The headless wallet daemon.
  - Exposes the BRC-100 HTTP interface on `http://127.0.0.1:3321`.
  - Manages private keys and UTXOs locally (SQLite).
  - Delegates permission requests to the Bridge.

- **`bridge/`**: The permission bridge service.
  - Exposes an internal API on `http://127.0.0.1:18790`.
  - Connects to the user's chat provider (Telegram).
  - Converts wallet permission requests into interactive chat prompts.

- **`pay/`**: A Node.js BRC-29 payment CLI.
  - Connects to the running wallet via `WalletClient('auto', 'pay')`.
  - `pay send <recipient> <satoshis>` — send to a hex identity key or name/email
  - `pay receive` — list and internalize inbound BRC-29 payments
  - `pay identity` — print your identity public key
  - Identity resolution via `IdentityClient` for non-hex recipients.

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
# Build the headless wallet daemon
cd core
go build -tags headless -o ../bin/gebunden .

# Build the permission bridge
cd ../bridge
go build -o ../bin/bridge .
```

The `headless` build tag strips all GUI dependencies. The resulting binary has no window, no tray icon, and no display requirement — it runs as a pure background service.

### 2. Run

Start the bridge first so the wallet has somewhere to send permission prompts, then start the wallet daemon.

```bash
# Start the permission bridge (reads from ~/.gebunden/bridge-config.json)
./bin/bridge &

# Start the headless wallet daemon
./bin/gebunden --headless &
```

Both processes bind exclusively to `127.0.0.1` and are not reachable from the network.

### 3. Verify

```bash
# Wallet liveness check
curl -s -X POST http://127.0.0.1:3321/isAuthenticated \
  -H "Content-Type: application/json" \
  -H "Origin: http://local" \
  -d '{}'
# Expected: {"authenticated":true}

# Bridge liveness check
curl -s http://127.0.0.1:18790/health
# Expected: {"ok":true}
```

### 4. Use

Any application on the machine can call the wallet over HTTP using the BRC-100 interface:

```bash
curl -X POST http://127.0.0.1:3321/getPublicKey \
  -H "Content-Type: application/json" \
  -H "Origin: http://my-app" \
  -d '{"protocolID":[1,"my protocol"],"keyID":"1"}'
```

If the action requires permission, a prompt arrives via Telegram. Tap **Approve** or **Deny**. The HTTP request blocks until you respond (default timeout: **180 seconds**).

Or use the `pay` CLI directly:

```bash
cd pay && npm install && npm run build && npm link
pay identity          # print your identity public key
pay send <key> 1000   # send 1000 sats
pay receive           # accept inbound payments
```

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
