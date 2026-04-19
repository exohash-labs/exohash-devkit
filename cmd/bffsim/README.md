# bffsim

Mock HTTP/SSE **Backend-For-Frontend** on `:3100`. Wraps [`chainsim`](../../chainsim) so the UI (or any HTTP client) gets the same endpoints as the real ExoHash BFF — with a block ticking every 500ms in memory.

## Why it exists

Frontend developers shouldn't need to run a Cosmos node. bffsim exposes the exact API surface the real BFF does (plus a few Cosmos REST mocks), so `ui/` can run unmodified against it.

## Run

```bash
go run ./cmd/bffsim               # from repo root
```

Config comes from [`config.yaml`](../../config.yaml) at the repo root — port, block time, bankroll deposit, games to register (with WASM paths), faucet amount, min-stake.

## Endpoints

**Same as real BFF:**

| Method | Path | Notes |
|--------|------|-------|
| GET  | `/stream` | SSE, supports `?games=` and `?address=` filters, replay buffer + live |
| POST | `/relay/place-bet` | `{address, bankrollId, calculatorId, stake, params}` |
| POST | `/relay/bet-action` | `{address, betId, action}` |
| POST | `/faucet/request` | `{address}` → 100 USDC |
| GET  | `/account/{addr}/balance` | |
| GET  | `/account/{addr}/bets?limit=N` | |
| GET  | `/bet/{id}/state` | Settlement + calc events |
| GET  | `/games` | `calcId, bankrollId, engine, houseEdgeBp, wasmHash, sourceUrl, errors` |
| GET  | `/game/{id}/info` | |
| GET  | `/health` | |

**Cosmos REST mocks** (for the UI's cosmjs signer — returns canned responses so no real chain is needed):

| Path | Returns |
|------|---------|
| `/cosmos/auth/v1beta1/accounts/{addr}` | `account_number=0, sequence=0, chain_id=exohash-solo-1` |
| `/cosmos/base/tendermint/v1beta1/node_info` | `chain_id=exohash-solo-1` |
| `/cosmos/tx/v1beta1/txs` | `txhash=BFFSIM_FAKE_TX` (broadcast no-op) |
| `/cosmos/authz/v1beta1/grants` | always-exists grant |

## Behavior

- **Block tick:** a goroutine calls `chain.AdvanceBlock()` every `blockTimeMs` (default 500)
- **SSE replay:** the last ~10k events are buffered per connection so a reconnecting client can catch up
- **CORS:** `Access-Control-Allow-Origin: *` (dev convenience)
- **State:** purely in-memory — restarting bffsim wipes bets, balances, and bet IDs
