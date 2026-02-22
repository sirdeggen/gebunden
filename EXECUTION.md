Gebunden - Execution Plan (OpenClaw bridge)

Goal
- Gut the existing gebunden repo, port to a headless wallet service with a Telegram-based permission prompt bridge for OpenClaw, while preserving Private Key handling and BRC-100 WalletInterface on localhost.

Scope
- Phase 0: Scaffolding, repo hygiene, environment checks
- Phase 1: Headless wallet HTTP API port and wallet initialization, same SQLite storage, key management
- Phase 2: Telegram bridge skeleton and integration point with OpenClaw
- Phase 3: Permission prompt translation (GUI wallet modals -> Telegram prompts with interactive buttons)
- Phase 4: End-to-end tests, MVP validation, and rollout plan

Phases & Tasks

Phase 0 — Scaffolding & kickoff
- [ ] Create a dedicated branch for headless-bridge work (e.g., gebunden/headless-bridge)
- [ ] Verify environment: Go 1.20+ (or current), dependencies installable, Telegram bot API token available
- [ ] Create a minimal skeleton for the Telegram bridge (no functionality yet)
- [ ] Document assumptions in EXECUTION.md

Phase 1 — Headless wallet port
- [ ] Keep the existing HTTP(S) WalletInterface server (localhost) as the primary interface
- [ ] Ensure private key loading via env/config (preserve existing wallet identity storage under ~/.bsv-desktop)
- [ ] Ensure wallet storage migrations and sqlite path logic intact
- [ ] Validate that standard WalletService calls (createAction, signAction, etc.) still work against a test harness

Phase 2 — Telegram bridge scaffold
- [ ] Implement a TelegramBridge module (Go or Node) with:
  - [ ] Bot token configuration via env
  - [ ] Simple /start handler and a dispatch endpoint for permission prompts
  - [ ] Endpoint to publish a Prompt to a Telegram chat and receive inline button presses via webhook
- [ ] Expose a small API for OpenClaw to push prompts and receive results
- [ ] Thread bridge to wallet service through HTTP call to wallet interface for results

Phase 3 — Permission translation
- [ ] Define data contract for permission prompts (id, origin, app, action, amount, asset, duration, etc.)
- [ ] Implement translation from WalletPermissionsManager GUI modal prompts to Telegram Prompt messages
- [ ] Implement inline keyboard actions (Approve/Reject/Send) and a way to report the decision back to the wallet service
- [ ] Add retry/backoff and audit logging

Phase 4 — Testing & MVP validation
- [ ] Basic unit tests for bridge contract and wallet method wrappers
- [ ] End-to-end test: trigger a sample permission prompt and confirm Telegram flow completes and wallet continues
- [ ] Security review: ensure no leakage of private keys, proper scoping, and permission sandboxing
- [ ] Documentation: how to configure and run the headless Gebunden bridge

Deliverables
- Execution.md updated with concrete steps
- gebunden-headless-bridge branch with initial skeleton code
- Telegram bridge prototype and integration hooks
- MVP validation report

Assumptions
- Telegram Bot API token is kept secret and not committed
- The existing wallet identity key is preserved at ~/.bsv-desktop and wallet storage remains sqlite-based
- OpenClaw channels (Telegram) are the primary user interaction channel for now

Risks
- Telegram latency or bot reliability affecting UX
- Complex permission flows may require iteration
- Potential API surface differences between Wails GUI and headless HTTP wallet interface

Next steps
- Confirm the plan and preferred language for the Telegram bridge (Go or Node).
- Confirm where to host the new bridge (same host as gebunden or separate service) and how to wire it to OpenClaw.