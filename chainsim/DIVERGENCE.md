# Chainsim ↔ Chain Divergence Log

Track every feature/behavior in chainsim that differs from `exohash/x/house/keeper`.
When aligning with the real chain, check each item.

## Active divergences

- [ ] **Calculator name uniqueness** — chainsim enforces unique names. Chain does NOT. Must add to chain (`msg_server_calculator.go`) when aligning.
- [ ] **Withdrawal delay** — chainsim withdrawals are instant. Chain has time-delayed withdrawal queue (`RequestWithdrawal` → `ProcessWithdrawals`).
- [ ] **Fee collection timing** — chainsim deducts fees at settlement. Chain does the same (EscrowFullStake at placement, CollectFeesFromEscrow at settlement). Verify math matches.
- [ ] **Share price calculation** — chainsim uses `amount * totalShares / balance`. Chain uses `MintShares()` which may have rounding differences.
- [ ] **RNG derivation** — chainsim uses `SHA256(seed || height)`. Chain uses BLS threshold beacon. Mock-bff tests won't catch RNG-dependent game behavior differences.
- [ ] **Authz/relay** — chainsim has no authz concept. Chain requires `MsgGrant` before relay can submit on behalf.
- [ ] **Gas/fees** — chainsim has no gas. Chain charges gas per tx.
- [ ] **Pending actions** — chainsim doesn't track pending actions in KV. Chain stores in `EngineKV`.

- [ ] **Crash join window** — chainsim/mock uses 16 blocks (8s) for dev. Chain default is 3 blocks. Sync when deploying.
- [ ] **Crash round_open event** — added close_height + join_window fields. Chain WASM must match.

## Resolved

(none yet)
