# BTC-style three-node development chain

Approved scope: independent development chain, retaining GoChainKit's plugin architecture and custom Ed25519 transactions/libp2p wire format. No Bitcoin wire/script compatibility.

## Architecture

Reuse model/utxo, protocol/composed-pow, rules/pow, branch ledger/importer, sync/chain and Bolt. Consensus currency issuance remains deterministic system coinbase outputs derived from chain binding, parent, height and producer; users cannot submit mint transactions. Reward plus fees is computed by replay, committed by state root, and subject to configurable maturity. No preallocation in the Docker network.

Add an optional contextual currency credit capability with an explicit height argument, keeping generic executors and other models independent of UTXO semantics. Maturity is bound into the UTXO model descriptor. Reward metadata and UTXOs live in the same branch snapshot and atomic ledger transaction, so reorgs and reopen restore both. Spend validation uses candidate height, not local time. Escrow cannot bypass maturity.

Add anchored address UTXO/balance queries and an encrypted-keystore wallet CLI: create/address/balance/send/transaction/status. Amounts use integer base units and string JSON fields. Wallet selects mature confirmed inputs deterministically, returns change and signs locally; keys never go to HTTP.

A devnet initializer creates three independent encrypted identities, miner wallets, matching genesis/rules, and node configurations with Docker DNS seeds. An init service owns initialization, then three node services use separate data directories in persistent volumes. No baked-in private keys. Restart reuses generated configuration and identities; initialization refuses inconsistent or partial state. API host mappings bind loopback.

## Development parameters

Initial subsidy 5,000,000,000 base units, halving every 150 blocks, reward maturity 5 blocks, retarget every 20 blocks, target spacing 2 seconds, easy bounded target. These intentionally shorten demonstrations; Bitcoin uses different production parameters. Reference: https://developer.bitcoin.org/devguide/block_chain.html

## Acceptance

Unit tests: maturity boundary, no-context fail closed, descriptor isolation, deterministic listing, ownership/overflow/double spend, reward/fees, halving and retarget existing rules.

Integration and Docker acceptance: three peers connect, all nodes mine and sync, locally signed transfer confirms on every node, invalid signatures rejected, reward maturity enforced, confirmed balance/transaction survive restart, an offline node catches up, partitioned branches converge by cumulative work after reconnection. Use bounded polling, include failure diagnostics, preserve operator volumes.

Run go test ./..., go test -race ./..., go vet ./..., Docker image build and real three-node acceptance. Report actual verification limits.
