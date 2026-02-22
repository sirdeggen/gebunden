---
layout: default
title: Home
---

# Gebunden

A headless BRC-100 wallet for [OpenClaw](https://openclaw.ai) agents.

Gebunden runs as a background service on your machine, exposing the standard BSV `WalletInterface` over localhost HTTP. Instead of a desktop GUI, it uses your existing chat channel (Telegram) to surface permission prompts — spend authorizations, protocol access, certificate requests — as interactive messages you approve or deny with a tap.

## How It Works

```
┌─────────────┐     HTTP      ┌──────────┐    Telegram    ┌──────┐
│  Your App   │ ──────────▸   │ Gebunden │  ◂──────────▸  │ You  │
│ (or Skill)  │  localhost    │  + Bridge │   Bot API      │      │
└─────────────┘   :3321       └──────────┘                 └──────┘
```

1. An app (or OpenClaw skill) calls the wallet via `http://localhost:3321`.
2. If the action needs permission, Gebunden pauses the request and sends a prompt to your Telegram.
3. You tap **Approve** or **Deny**.
4. Gebunden resumes or rejects the action, and the app gets its response.

## Quick Links

- [Installation Guide (SKILL.md)](SKILL.md) — For OpenClaw agents: how to download, install, and run Gebunden.
- [GitHub Repository](https://github.com/sirdeggen/gebunden)
- [OpenClaw](https://openclaw.ai)

## Components

| Directory | Description |
|-----------|-------------|
| `core/`   | Headless wallet daemon — BRC-100 HTTP interface on localhost |
| `bridge/` | Permission bridge — routes prompts to Telegram (or other channels) |

## Requirements

- OpenClaw with Telegram channel configured
- Go 1.22+ (for building from source) or pre-built binaries from [Releases](https://github.com/sirdeggen/gebunden/releases)

## License

See [LICENSE](https://github.com/sirdeggen/gebunden/blob/main/LICENSE).
