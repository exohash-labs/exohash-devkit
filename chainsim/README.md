# chainsim

In-memory Go simulator of the ExoHash chain — same WASM execution semantics, same bankroll accounting, same beacon-driven randomness as the real chain, but with no consensus, no networking, and no persistence.

Used by:
- **Game test suites** (`games/*/main.go`) — simulate N bets, assert house edge / gas / error paths
- **bffsim** (`cmd/bffsim/`) — wraps chainsim behind an HTTP/SSE BFF for UI development

## What it models

- **Accounts** with uusdc balances and deposit/withdraw
- **Bankrolls** with per-game attach/detach, deposits, solvency caps
- **Calculators** (WASM modules) — loaded, hashed, and executed via [wazero](https://github.com/tetratelabs/wazero)
- **Block advancement** — one block per `AdvanceBlock()` call; beacon RNG is deterministic from `(seed, height)`
- **Gas metering** — bytecode is instrumented at load with gas-charge injection; per-calc gas balance enforced
- **Bets** — place, multi-action, settle; calc events + system events collected per block

## What it doesn't model

See [`DIVERGENCE.md`](DIVERGENCE.md) for the authoritative list — things like IBC, governance, DKG handshake,
slashing, and real validator rotation are stubbed or absent. The WASM→chain interface is 1:1 with the real chain.

## Usage (direct)

```go
import "github.com/exohash-labs/exohash-devkit/chainsim"

chain := chainsim.NewChain(chainsim.DefaultParams())
calcID := chain.RegisterCalculator(wasmBytes)
bankrollID := chain.CreateBankroll("house", depositUUsdc)
chain.AttachGame(bankrollID, calcID, houseEdgeBp)

chain.Deposit(playerAddr, 100_000_000)
betID, _ := chain.PlaceBet(playerAddr, bankrollID, calcID, stake, params)

result := chain.AdvanceBlock() // next block — settles dice, emits calc events
```

## Running tests

```bash
go test ./chainsim/...
```
