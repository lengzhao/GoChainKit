# Three-node BTC-style devnet Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** A complete independent UTXO currency demo on three persistent Docker nodes.

**Architecture:** Reuse composed PoW and existing branch/sync plugins. Extend model-specific currency maturity and queries, add operator CLI and generated Docker configuration.

**Tech Stack:** Go 1.26.1, PluginKit, libp2p, bbolt, Ed25519, Docker Compose.

---

### Task 1: Maturity and anchored currency metadata

Files: cap/asset/asset.go, model/utxo/utxo.go, model/utxo/coins.go, model/utxo/maturity_test.go, protocol/composed/executor.go.

1. Write failing tests for a reward minted at height 1: spending before height 6 is deferred, at height 6 succeeds, missing context fails closed. Verify parent state unchanged.
2. Run `GOSUMDB=sum.golang.org go test ./model/utxo` and confirm missing capability failure.
3. Add optional contextual credit capability, maturity-bound model identity, reward metadata and deterministic owner listing. Use candidate height and checked arithmetic. Preserve default maturity-zero behavior.
4. Pass execution height through optional credit capability. Refuse context-free escrow with maturity enabled.
5. Run model and composed tests, including reward/fee/reorg/recovery tests.

### Task 2: Address queries and wallet

Files: api/http/utxo/routes.go, api/http/utxo/routes_test.go, wallet/wallet.go, wallet/wallet_test.go, cmd/gochainkit-wallet/main.go.

1. Add failing tests for anchored owner UTXO listing, balance/maturity, invalid address, empty wallet, deterministic selection/change/fee, malformed server responses.
2. Implement address response with head/height, spendable and immature totals and ordered coins, bounded paging.
3. Implement encrypted wallet creation, address display, balance and signed submission; parameter validation, bounded HTTP requests and useful errors.
4. Run `GOSUMDB=sum.golang.org go test ./wallet ./api/http/... ./cmd/gochainkit-wallet`.

### Task 3: Repeatable devnet initialization

Files: devnet/init.go, devnet/init_test.go, cmd/gochainkit-devnet/main.go.

1. Add failing tests for three generated configs with matching protocol and distinct keys/paths, restart reuse, partial initialization rejection and password failure.
2. Generate random encrypted P2P identities/wallets, DNS seeds and node configs using typed model/rule configuration. Store manifest last, never overwrite existing material.
3. Add health/status command for container healthchecks.
4. Test initialization and real PluginKit config assembly.

### Task 4: Docker orchestration and acceptance

Files: Dockerfile, compose.yaml, .dockerignore, scripts/devnet-smoke.py, docs/docker-devnet.md, README.md.

1. Build multistage static Go binaries, non-root runtime, one init service and three independent node services with healthchecks and persistent storage.
2. Add bounded acceptance runner covering connectivity, reward, transfer, invalid submission, restart, catch-up and partition/rejoin.
3. Run full test, race and vet checks, `docker compose config`, build and real acceptance. Inspect logs for failures.
4. Document exact launch/wallet/acceptance commands, shortened consensus parameters, system coinbase semantics and compatibility boundaries.
