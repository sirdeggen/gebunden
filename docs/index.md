---
layout: page
title: SKILL.md
---

# Gebunden — OpenClaw Skill Instructions

## What This Is

Gebunden is a headless BSV wallet that runs on the user's machine and exposes the BRC-100 `WalletInterface` over `http://localhost:3321`. Permission prompts (spending, protocol access, certificates, etc.) are surfaced through a dedicated Telegram bot as interactive messages with inline Approve/Deny buttons.

This document tells you, the OpenClaw agent, how to install and run Gebunden for your user.

## Prerequisites

- OpenClaw is installed and running.
- A **dedicated Telegram bot** for Gebunden (separate from the OpenClaw bot — see Setup below).
- The user has a wallet identity file, or you will generate one during setup.

> **Important:** The Gebunden bridge uses its own Telegram bot, not the OpenClaw bot. Using the same bot causes `getUpdates` conflicts. Create a separate bot via @BotFather.

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

## Setup

### 1. Create Your Gebunden Bot (30 seconds)

Each user needs their own Telegram bot for wallet prompts. This keeps your wallet isolated and avoids conflicts with OpenClaw's bot.

1. Message [@BotFather](https://t.me/BotFather) → `/newbot`
2. Name it anything (e.g. "My Wallet")
3. Pick a username (e.g. `myname_wallet_bot`)
4. Copy the token
5. Open your new bot in Telegram and tap **Start**

That's it.

### 2. Configure the Bridge

Save the bot token and your Telegram chat ID:

```bash
cat > ~/.gebunden/bridge-config.json << EOF
{
  "telegramBotToken": "<YOUR_BOT_TOKEN>",
  "telegramChatID": "<YOUR_TELEGRAM_USER_ID>"
}
EOF
chmod 600 ~/.gebunden/bridge-config.json
```

> **Finding your chat ID:** Your Telegram user ID is visible in OpenClaw's inbound message metadata (`sender_id`). Or message [@userinfobot](https://t.me/userinfobot) on Telegram.

> **Security:** Never commit `bridge-config.json` to a repository. It contains your bot token.

Alternative: use environment variables instead of the config file:
```bash
export GEBUNDEN_BOT_TOKEN="<YOUR_BOT_TOKEN>"
export GEBUNDEN_CHAT_ID="<YOUR_TELEGRAM_USER_ID>"
```

### 3. Wallet Identity

Gebunden needs a root private key to operate. It searches in this order:

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

> **Security:** The wallet identity file contains the root private key. Never commit it, share it, or transmit it over the network.

## Running

Start the bridge first, then the wallet daemon.

```bash
# Start the bridge (reads config from ~/.gebunden/bridge-config.json)
~/.gebunden/bin/gebunden-bridge &

# Start the wallet in headless mode
~/.gebunden/bin/gebunden --headless &
```

### Verify It's Running

```bash
# Bridge health check
curl -s http://127.0.0.1:18790/health
# Expected: {"ok":true}

# Wallet — check authentication
curl -s -X POST http://127.0.0.1:3321/isAuthenticated \
  -H "Content-Type: application/json" \
  -H "Origin: http://test" \
  -d '{}'
# Expected: {"authenticated":true}
```

## Using the Wallet from Skills

Any OpenClaw skill can call the wallet via localhost HTTP. The wallet implements the full BRC-100 `WalletInterface`.

### Example: Get Public Key

```bash
curl -X POST http://127.0.0.1:3321/getPublicKey \
  -H "Content-Type: application/json" \
  -H "Origin: http://my-skill" \
  -d '{"protocolID":[1,"my protocol"],"keyID":"1"}'
```

This triggers a Telegram prompt from the Gebunden bot:

> 🔗 **Protocol Access Request**
>
> **App:** `my-skill`
> **Protocol:** my protocol
> **Security Level:** 1
>
> `[🔗 Grant Access]` `[❌ Deny]`

The HTTP request blocks until the user taps a button (up to 180 seconds).

### Example: Create a Transaction

```bash
curl -X POST http://127.0.0.1:3321/createAction \
  -H "Content-Type: application/json" \
  -H "Origin: http://my-skill" \
  -d '{
    "description": "Send 100 sats",
    "outputs": [{"satoshis": 100, "lockingScript": "..."}]
  }'
```

> 💸 **Spending Authorization**
>
> **App:** `my-skill`
> **Amount:** 100 sats
> **Description:** Send 100 sats
>
> `[💸 Send]` `[❌ Deny]`

### Important: Protocol ID Format

The BRC-100 SDK expects `protocolID` as a JSON array `[securityLevel, "protocolName"]`, **not** an object:
- ✅ `"protocolID": [1, "my protocol"]`
- ❌ `"protocolID": {"protocol": "my protocol", "securityLevel": 1}`

Protocol names can only contain letters, numbers, and spaces.

### Permission Types

| Type | Trigger | Prompt |
|------|---------|--------|
| **Spend** | `createAction`, `signAction`, `internalizeAction` | Amount, description, app name |
| **Protocol** | `encrypt`, `decrypt`, `createHmac`, `createSignature`, `getPublicKey` | Protocol ID, security level |
| **Certificate** | `acquireCertificate`, `proveCertificate` | Certificate type, verifier |
| **Basket** | `listOutputs`, `relinquishOutput` | Basket name |
| **Counterparty** | `revealCounterpartyKeyLinkage`, `revealSpecificKeyLinkage` | Counterparty key, verifier |

Read-only methods (`listActions`, `listCertificates`, `discoverByAttributes`, etc.) do not require permission.

## Ports

| Service | Port | Purpose |
|---------|------|---------|
| Wallet HTTP | 3321 | BRC-100 WalletInterface (localhost only) |
| Bridge | 18790 | Permission bridge + Telegram relay (localhost only) |

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
| `--telegram-token` | (from config) | Override Telegram Bot Token |
| `--telegram-chat` | (from config) | Override Telegram chat ID |

## Troubleshooting

- **No Telegram prompt received:** Check that the bridge is running (`curl http://127.0.0.1:18790/health`), the bot token is valid, and you've started a chat with the Gebunden bot.
- **Permission timeout (180s):** The wallet returns an error to the calling app. The user can retry.
- **Bridge unreachable:** The wallet denies the request by default for safety. Ensure the bridge is started before the wallet.
- **Key file not found:** Ensure `~/.gebunden/wallet-identity.json` exists with a valid `rootKeyHex`.
- **OpenClaw messages stop working:** You're using the same bot token for both OpenClaw and Gebunden. Create a separate bot for Gebunden.

## Data Storage

All wallet data lives under `~/.gebunden/`:

```
~/.gebunden/
├── bridge-config.json            # Telegram bot token + chat ID (chmod 600)
├── wallet-identity.json          # Root private key (chmod 600)
├── wallet-<id>-main.sqlite       # UTXO and transaction database
└── settings.json                 # Wallet settings
```

## Extending to Other Channels

The bridge is designed as a standalone service. To add WhatsApp, Discord, or another channel:

1. Copy `bridge/main.go` as a starting point.
2. Replace the Telegram API calls with your channel's API.
3. Keep the same HTTP contract (`POST /request-permission`, `POST /respond`, `GET /pending`).
4. Point the wallet at your new bridge with `--bridge-url`.

The `GET /pending` and `POST /respond` endpoints remain available for any channel that prefers a polling/webhook pattern over direct API integration.
