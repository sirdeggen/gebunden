# Gebunden — OpenClaw Skill Instructions

## What This Is

Gebunden is a headless BSV wallet that runs on the user's machine and exposes the BRC-100 `WalletInterface` over `http://localhost:3321`. Permission prompts (spending, protocol access, certificates, etc.) are surfaced through Telegram as interactive messages with Approve/Deny buttons.

This document tells you, the OpenClaw agent, how to install and run Gebunden for your user.

## Prerequisites

- OpenClaw is installed and running.
- Telegram channel is configured (`channels.telegram.botToken` exists in `~/.openclaw/openclaw.json`).
- The user has a wallet identity file, or you will generate one during setup.

## Installation

### Option A: Pre-built Binaries (Recommended)

Download the latest release for your platform:

```bash
# Detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

# Download latest release
RELEASE_URL="https://github.com/sirdeggen/gebunden/releases/latest/download"
mkdir -p ~/.gebunden/bin

curl -fSL "${RELEASE_URL}/gebunden-${OS}-${ARCH}" -o ~/.gebunden/bin/gebunden
curl -fSL "${RELEASE_URL}/gebunden-bridge-${OS}-${ARCH}" -o ~/.gebunden/bin/gebunden-bridge

chmod +x ~/.gebunden/bin/gebunden ~/.gebunden/bin/gebunden-bridge
```

### Option B: Build from Source

Requires Go 1.22+.

```bash
git clone https://github.com/sirdeggen/gebunden.git /tmp/gebunden
mkdir -p ~/.gebunden/bin

# Build wallet daemon
cd /tmp/gebunden/core
go build -o ~/.gebunden/bin/gebunden .

# Build bridge
cd /tmp/gebunden/bridge
go build -o ~/.gebunden/bin/gebunden-bridge .

rm -rf /tmp/gebunden
```

## Configuration

### Wallet Identity

Gebunden needs a root private key to operate. It searches for an identity file in this order:

1. Path given by `--key-file` flag
2. `GEBUNDEN_PRIVATE_KEY` environment variable (hex-encoded root key)
3. `~/.gebunden/wallet-identity.json`
4. `~/.clawdbot/bsv-wallet/wallet-identity.json` (legacy fallback)

If the user doesn't have a wallet identity yet, create one:

```bash
cat > ~/.gebunden/wallet-identity.json << 'EOF'
{
  "rootKeyHex": "<64-char hex private key>",
  "network": "mainnet"
}
EOF
chmod 600 ~/.gebunden/wallet-identity.json
```

> **Security:** The wallet identity file contains the root private key. Never commit it, share it, or transmit it over the network. Set permissions to `600`.

### Telegram Bridge

The bridge reads the Telegram Bot Token automatically from the OpenClaw config:

- **Config path:** `~/.openclaw/openclaw.json`
- **Key:** `channels.telegram.botToken`

No additional Telegram configuration is needed if OpenClaw is already paired with Telegram.

The bridge also needs to know which chat to send prompts to. Pass this via:
- `--telegram-chat <CHAT_ID>` flag, or
- `TELEGRAM_CHAT_ID` environment variable

You can find the chat ID from the OpenClaw inbound message metadata (`sender_id` in Telegram context).

## Running

Start the bridge first, then the wallet daemon.

```bash
# Start the bridge (reads Telegram token from OpenClaw config)
~/.gebunden/bin/gebunden-bridge --telegram-chat "<USER_CHAT_ID>" &

# Start the wallet in headless mode
~/.gebunden/bin/gebunden --headless &
```

### Verify It's Running

```bash
# Bridge health check
curl -s http://127.0.0.1:18790/health
# Expected: {"ok":true}

# Wallet — get public key (should work without permission prompt)
curl -s -X POST http://127.0.0.1:3321/getPublicKey \
  -H "Content-Type: application/json" \
  -d '{"protocolID":{"protocol":"identity","securityLevel":1},"keyID":"1"}'
```

## Using the Wallet from Skills

Any OpenClaw skill can call the wallet via localhost HTTP. The wallet implements the full BRC-100 `WalletInterface`.

### Example: Create a Transaction

```bash
curl -X POST http://127.0.0.1:3321/createAction \
  -H "Content-Type: application/json" \
  -H "X-Origin: my-skill" \
  -d '{
    "description": "Send 100 sats",
    "outputs": [{"satoshis": 100, "lockingScript": "..."}]
  }'
```

This will trigger a Telegram prompt:

> 💸 **Spending Authorization**
>
> App: `my-skill`
> Amount: 100 sats
> Description: Send 100 sats
>
> `[💸 Send]` `[❌ Deny]`

The HTTP request blocks until the user taps a button (up to 180 seconds).

### Permission Types

| Type | Trigger | Prompt |
|------|---------|--------|
| **Spend** | `createAction`, `signAction`, `internalizeAction` | Amount, description, app name |
| **Protocol** | `encrypt`, `decrypt`, `createHmac`, `createSignature`, `getPublicKey` | Protocol ID, security level, counterparty |
| **Certificate** | `acquireCertificate`, `proveCertificate` | Certificate type, verifier |
| **Basket** | `listOutputs`, `relinquishOutput` | Basket name |
| **Counterparty** | `revealCounterpartyKeyLinkage`, `revealSpecificKeyLinkage` | Counterparty key, verifier |

Read-only methods (`listActions`, `listCertificates`, `discoverByAttributes`, etc.) do not require permission.

## Ports

| Service | Port | Purpose |
|---------|------|---------|
| Wallet HTTP | 3321 | BRC-100 WalletInterface (localhost only) |
| Bridge | 18790 | Internal bridge API (localhost only) |

Both bind to `127.0.0.1` — they are not accessible from the network.

## Flags Reference

### `gebunden` (wallet daemon)

| Flag | Default | Description |
|------|---------|-------------|
| `--headless` | `false` | Run without GUI |
| `--auto-approve` | `false` | Skip permission prompts (development only) |
| `--key-file` | (auto-detect) | Path to wallet identity JSON |
| `--bridge-url` | `http://127.0.0.1:18790` | Bridge service URL |

### `gebunden-bridge`

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `18790` | Bridge listen port |
| `--telegram-token` | (from OpenClaw config) | Override Telegram Bot Token |
| `--telegram-chat` | (from env) | Telegram chat ID for prompts |

## Troubleshooting

- **No Telegram prompt received:** Check that the bridge is running and the bot token is valid. Run `curl http://127.0.0.1:18790/health`.
- **Permission timeout (180s):** The wallet returns an error to the calling app. The user can retry.
- **Bridge unreachable:** The wallet denies the request by default for safety. Ensure the bridge is started before the wallet.
- **Key file not found:** Ensure `~/.gebunden/wallet-identity.json` exists with a valid `rootKeyHex`.

## Data Storage

All wallet data lives under `~/.gebunden/`:

```
~/.gebunden/
├── wallet-identity.json          # Root private key (chmod 600)
├── wallet-<id>-mainnet.sqlite    # UTXO and transaction database
└── settings.json                 # Wallet settings
```

## Extending to Other Channels

The bridge is designed as a standalone service. To add WhatsApp, Discord, or another channel:

1. Copy `bridge/main.go` as a starting point.
2. Replace the Telegram API calls with your channel's API.
3. Keep the same HTTP contract (`POST /request-permission`, `POST /respond`).
4. Point the wallet at your new bridge with `--bridge-url`.
