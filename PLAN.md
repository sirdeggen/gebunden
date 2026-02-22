# Gebunden - Headless BSV Wallet for OpenClaw

## Vision
Transform the `gebunden` GUI wallet into a headless, localhost-native BRC-100 wallet provider. It will serve as the "system wallet" for OpenClaw skills, using Telegram for user interactions (permissions, confirmations) instead of a desktop window.

## Architecture

### 1. Headless Wallet Service (Go)
- **Base:** Forked from `sirdeggen/gebunden`.
- **Modifications:**
  - Remove Wails/Frontend dependency for core operation.
  - Run `HTTPServer` (Babbage-compatible interface) as the primary entry point.
  - Inject an `InterventionHandler` into `WalletService` to intercept BRC-100 method calls.

### 2. Intervention Logic
- **Trigger:** When an external app (via localhost HTTP) calls a sensitive method (e.g. `createAction`, `signAction`) or requests a new permission.
- **Action:**
  - Suspend the HTTP request.
  - Generate a unique `request_id`.
  - Send an "Approval Request" payload to the OpenClaw Agent (via webhook/IPC).
  - Wait for resolution.

### 3. OpenClaw Skill (`gebunden-skill`)
- **Role:** Orchestrator & UI Bridge.
- **Functions:**
  - **Runner:** Starts the `gebunden` binary in the background.
  - **Watcher:** Listens for intervention requests (via HTTP endpoint or stdout).
  - **UI:** Formats a Telegram message with details ("App X wants to spend 500 sats") and buttons (`[Approve]`, `[Reject]`).
  - **Callback:** Handling the button click resumes the suspended wallet request with the result.

## Data & Config
- **Identity:** Load keys from `~/.clawdbot/bsv-wallet/wallet-identity.json` (preserve existing wallet).
- **Storage:** SQLite (headless).
- **Network:** Mainnet.

## Roadmap

### Phase 1: Core Adaptation (Current)
- [ ] Create `gebunden-skill` skeleton.
- [ ] Modify `gebunden` Go code:
    - [ ] Add `HeadlessMode` flag.
    - [ ] Implement `InterventionHandler` interface.
    - [ ] Insert hooks into `WalletService` methods.
- [ ] Build `gebunden` binary.

### Phase 2: OpenClaw Integration
- [ ] Implement the `gebunden-skill` logic to handle approval flows.
- [ ] Test with `search_web` or other skills utilizing the wallet.

### Phase 3: Refinement
- [ ] "Always allow" policies (optional).
- [ ] Richer transaction parsing in Telegram.

## Config
- **Repo:** `workspace/gebunden/gebunden-src`
- **Output Binary:** `workspace/gebunden/bin/gebunden-d`
- **Port:** 3321 (HTTP), 2121 (HTTPS)
