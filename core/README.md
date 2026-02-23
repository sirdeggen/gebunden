# Gebunden Core

The headless BRC-100 wallet daemon. Runs as a background service with no GUI, no window, and no display requirement. Exposes the full [BRC-100](https://brc.dev/100) `WalletInterface` over localhost HTTP so any application on the machine can use it.

Permission prompts (spending, protocol access, certificates, etc.) are delegated to the [Bridge service](../bridge/) rather than a GUI dialog. The HTTP request blocks until the user approves or denies via their configured chat channel (Telegram).

## Architecture

```
┌─────────────────────────────────────────────┐
│              Gebunden Core (headless)        │
│                                             │
│  ┌──────────────────────────────────────┐   │
│  │           WalletService              │   │
│  │  (BRC-100 method dispatcher)         │   │
│  └──────────────┬───────────────────────┘   │
│                 │                           │
│  ┌──────────────▼───────────────────────┐   │
│  │           HTTPServer                 │   │
│  │   HTTP  127.0.0.1:3321               │   │
│  │   HTTPS 127.0.0.1:2121 (self-signed) │   │
│  └──────────────────────────────────────┘   │
│                                             │
│  ┌──────────────────────────────────────┐   │
│  │        BridgePermissionGate          │   │
│  │  POST 127.0.0.1:18790/request-perm.  │   │
│  │  Blocks until user approves/denies   │   │
│  └──────────────────────────────────────┘   │
│                                             │
│  ┌──────────────────────────────────────┐   │
│  │        GORM + SQLite Storage         │   │
│  │  ~/.gebunden/wallet-<key>-main.sqlite│   │
│  └──────────────────────────────────────┘   │
└─────────────────────────────────────────────┘
```

**WalletService** — Implements the full BRC-100 wallet interface: actions, outputs, certificates, cryptography, key derivation, and identity discovery.

**HTTPServer** — Serves all BRC-100 methods as `POST /<methodName>` endpoints. Requires an `Origin` or `Originator` header on every request (used as the app identifier in permission prompts). Full CORS support.

**BridgePermissionGate** — For any sensitive operation, serialises a `PermissionRequest` and POSTs it to the Bridge service at `http://127.0.0.1:18790/request-permission`. The call blocks (up to 130 seconds) until the bridge returns an approve/deny response. If the bridge is unreachable, the request is denied by default.

## Prerequisites

- **Go** 1.22+ ([go.dev](https://go.dev/dl/))
- CGO enabled (required for SQLite via `modernc.org/sqlite`)

No Node.js, no Wails CLI, no display server required.

## Build

```bash
cd core
go build -tags headless -o ../bin/gebunden .
```

The `headless` build tag excludes all Wails/GUI code paths. The binary has no dependency on a display server and can run in a terminal, as a systemd service, or in a Docker container.

## Configuration

### Wallet Identity

The daemon needs a root private key to derive all wallet keys. It searches in this order:

1. `--key-file <path>` flag
2. `GEBUNDEN_PRIVATE_KEY` environment variable (hex-encoded root key)
3. `~/.gebunden/wallet-identity.json`
4. `~/.clawdbot/bsv-wallet/wallet-identity.json` (legacy fallback)

The identity file format:

```json
{
  "rootKeyHex": "<64-char hex private key>",
  "network": "mainnet"
}
```

> **Security:** This file contains your root private key. Set permissions to `600` and never commit it.

### Bridge URL

The daemon forwards all permission requests to the Bridge service. Default: `http://127.0.0.1:18790`.

Override with `--bridge-url <url>`.

## Running

```bash
# Standard mode — prompts forwarded to Bridge
./bin/gebunden

# Auto-approve mode — no prompts, all requests granted (development only)
./bin/gebunden --auto-approve

# Custom key file and bridge URL
./bin/gebunden --key-file /path/to/wallet-identity.json --bridge-url http://127.0.0.1:18790
```

The daemon logs to stdout in structured text format and blocks until it receives `SIGINT` or `SIGTERM`.

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--auto-approve` | `false` | Approve all permission requests automatically |
| `--key-file` | `""` | Path to `wallet-identity.json` |
| `--bridge-url` | `http://127.0.0.1:18790` | URL of the Bridge permission service |

## HTTP Interface

All BRC-100 methods are served as `POST /<methodName>` on:

- **HTTP**: `http://127.0.0.1:3321`
- **HTTPS**: `https://127.0.0.1:2121` (self-signed certificate, auto-generated and installed to system trust store)

Every request must include an `Origin` or `Originator` header — this identifies the calling application in permission prompts.

### Supported Methods

| Category | Methods |
|----------|---------|
| **Actions** | `createAction`, `signAction`, `abortAction`, `listActions`, `internalizeAction` |
| **Outputs** | `listOutputs`, `relinquishOutput` |
| **Certificates** | `acquireCertificate`, `listCertificates`, `proveCertificate`, `relinquishCertificate` |
| **Cryptography** | `encrypt`, `decrypt`, `createHmac`, `verifyHmac`, `createSignature`, `verifySignature` |
| **Keys** | `getPublicKey`, `revealCounterpartyKeyLinkage`, `revealSpecificKeyLinkage` |
| **Discovery** | `discoverByIdentityKey`, `discoverByAttributes` |
| **Network** | `getHeight`, `getHeaderForHeight`, `getNetwork`, `getVersion` |
| **Auth** | `isAuthenticated`, `waitForAuthentication` |

### Permission Flow

Sensitive methods (`createAction`, `getPublicKey`, `encrypt`, `acquireCertificate`, etc.) trigger a permission check:

1. Core serialises a `PermissionRequest` (type, app, message, amount) and POSTs it to `http://127.0.0.1:18790/request-permission`
2. The Bridge delivers the prompt to the user (Telegram inline keyboard)
3. The Bridge blocks until the user taps **Approve** or **Deny** (up to 120 seconds)
4. Core receives the response and either completes or rejects the wallet operation
5. If the Bridge is unreachable, the request is **denied by default**

Read-only methods (`listActions`, `discoverByAttributes`, `isAuthenticated`, etc.) bypass the permission gate entirely.

## Data Storage

```
~/.gebunden/
├── wallet-<identityKey>-main.sqlite   # Wallet database (mainnet)
├── wallet-<identityKey>-test.sqlite   # Wallet database (testnet)
└── certs/
    ├── server.crt                     # Self-signed TLS certificate
    └── server.key                     # TLS private key
```

## Source Files

| File | Purpose |
|------|---------|
| `main.go` | Entry point, flag parsing, wallet init, signal handling |
| `wallet_service.go` | BRC-100 method dispatcher |
| `wallet_args.go` | JSON type aliases for SDK deserialization |
| `http_server.go` | BRC-100 HTTP/HTTPS server with CORS middleware |
| `permissions.go` | `PermissionGate` interface and `BridgePermissionGate` implementation |
| `ssl_cert.go` | Self-signed TLS certificate generation and system trust store installation |
| `storage_proxy_service.go` | GORM/SQLite storage layer |

## Testing

```bash
go test -tags headless ./...
```

## License

See [LICENSE](../LICENSE) for details.
