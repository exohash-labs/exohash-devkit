# ExoHash DevKit

Development toolkit for the [ExoHash](https://exohash.io) protocol — a Cosmos L1 blockchain for provably fair gaming.

## What is ExoHash

ExoHash uses **BLS threshold signatures** to produce unbiasable randomness every block. Validators run a Distributed Key Generation protocol — no single party can predict or manipulate the outcome. Game logic runs as **WebAssembly modules on-chain**, and anyone can create **permissionless USDC liquidity pools** (bankrolls) that back games and earn house edge.

The result: a decentralized casino protocol where cheating is cryptographically impossible.

## What is this repo

Everything you need to build and test ExoHash games and frontends locally — **without running a node**:

- **`chainsim/`** — in-memory Go simulator with real WASM execution, beacon randomness, bankroll accounting
- **`games/`** — 3 reference games (dice, crash, mines) — Go source + compiled WASM + test suite per game
- **`cmd/bffsim/`** — mock HTTP/SSE BFF on `:4000` that wraps chainsim; Cosmos REST endpoints mocked so the UI runs unchanged
- **`ui/`** — full Next.js casino frontend (snapshot of `exohash-play`) — wallet, signer, SSE feed, game pages
- **`bots/` + `cmd/bot-runner/`** — 15 configurable bots that place live bets across all three games
- **`CLAUDE_PROMPT.md`** — full developer reference (protocol, WASM API, game patterns, complete dice template)

## Quick demo

```bash
./start_demo.sh              # builds + runs bffsim :4000, UI :3001, 15 bots
./start_demo.sh --no-bots    # skip the bots
./start_demo.sh --dev        # `npm run dev` instead of prod build (hot reload)
./start_demo.sh stop         # shut everything down
./start_demo.sh logs         # tail bffsim/ui/bots logs together
```

Logs land in `.devkit/{bffsim,ui,bots}.log`. The three services are described individually below.

## Three user journeys

### 1. Build a new game

Write Go, compile with TinyGo, test against chainsim.

```bash
# Run the shipped test suites — house edge, error semantics, gas accounting
cd games/dice && go run .
cd games/mines && go run .
cd games/crash && go run .

# Build your own
cd games/dice
tinygo build -o dice.wasm -target=wasi -no-debug -opt=2 .
```

Start from the complete dice template in [`CLAUDE_PROMPT.md`](CLAUDE_PROMPT.md). Or paste that file into Claude / ChatGPT and say: *"Build me a blackjack game."*

### 2. Build a new frontend

Run the mock backend, develop against real chain behavior:

```bash
# Terminal 1 — mock chain + BFF on :4000
go run ./cmd/bffsim

# Terminal 2 — reference UI on :3001
cd ui
npm install
npm run build && npm start -- --port 3001   # prod bundle, ~10ms page loads
# or: npm run dev -- --port 3001            # Turbopack dev (slower)
```

Open `http://localhost:3001/dice`. Click *Create Wallet* → *Get test USDC* → *Authorize* → place bets. The bffsim ticks one block every 500ms; dice settles on the next block.

The `ui/` directory is a **snapshot of [exohash-play](https://github.com/exohash-labs/exohash-play)** — fork it, rip out what you don't need, or use it as reference for your own frontend.

### 3. Live activity — spin up bots

```bash
# Terminal 3 — 15 bots across dice, crash, mines
go run ./cmd/bot-runner
```

Bot strategies (stake, cashout multiplier, frequency) live in [`bots.yaml`](bots.yaml). With bffsim, the runner auto-generates fresh addresses and funds them via the mock faucet — no keyring setup required.

## Full-stack integration (optional)

For an end-to-end run against the **real chain** (not chainsim), clone the main
[exohash](https://github.com/exohash-labs/exohash) repo and run `scripts/run_all.sh` — it
brings up `exohashd`, the real BFF on `:3100`, 15 authz-funded bots, and the play
UI against a single-node devnet. Linux/amd64 only.

## Repository layout

```
CLAUDE_PROMPT.md       Full developer reference — protocol, WASM API, game patterns, dice template
chainsim/              In-memory chain simulator with WASM game execution
games/                 Reference games (source + compiled WASM + test suite)
  dice/                Solo instant — bet, roll, settle next block
  mines/               Solo multi-step — reveal tiles, cashout anytime
  crash/               Multiplayer session — rising multiplier, multi-player cashouts
cmd/bffsim/            Mock HTTP/SSE BFF (:4000) wrapping chainsim
cmd/bot-runner/        Bot runner process (reads bots.yaml)
bots/                  Bot implementations (dice, crash, mines) + SSE client
ui/                    Next.js casino frontend (exohash-play snapshot)
config.yaml            chainsim + bffsim configuration (games, bankroll, block time)
bots.yaml              Bot strategies
```

## API endpoints (bffsim)

| Method | Path | Description |
|--------|------|-------------|
| GET  | `/stream?games=1,2&address=x` | SSE event stream (replay + live) |
| POST | `/relay/place-bet`            | Place a bet (relay-signed) |
| POST | `/relay/bet-action`           | Player action (reveal, cashout, hit…) |
| POST | `/faucet/request`             | Fund test account (100 USDC) |
| GET  | `/account/{addr}/balance`     | Account balance |
| GET  | `/account/{addr}/bets`        | Player bet history |
| GET  | `/bet/{id}/state`             | Bet state + calc events (cold start) |
| GET  | `/games`                      | Registered games (calcId, bankrollId, wasmHash, edge) |
| GET  | `/game/{id}/info`             | Single game details |
| GET  | `/health`                     | Server status |
| GET  | `/cosmos/auth/v1beta1/accounts/{addr}` | Cosmos account mock (for UI signer) |
| GET  | `/cosmos/base/tendermint/v1beta1/node_info` | Cosmos node info mock |
| POST | `/cosmos/tx/v1beta1/txs`      | Cosmos broadcast mock |
| GET  | `/cosmos/authz/v1beta1/grants` | authz grant existence mock |

## Links

- [exohash.io](https://exohash.io)
- [Discord](https://t.co/0bYTwICtzp)
- [FAQ](https://exohash.io/faq)

## License

MIT
