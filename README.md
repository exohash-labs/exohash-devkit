# ExoHash DevKit

Development toolkit for the [ExoHash](https://exohash.io) protocol — a Cosmos L1 blockchain for provably fair gaming.

## What is ExoHash

ExoHash uses **BLS threshold signatures** to produce unbiasable randomness every block. Validators run a Distributed Key Generation protocol — no single party can predict or manipulate the outcome. Game logic runs as **WebAssembly modules on-chain**, and anyone can create **permissionless USDC liquidity pools** (bankrolls) that back games and earn house edge.

The result: a decentralized casino protocol where cheating is cryptographically impossible.

## What is this repo

Everything you need to build and test ExoHash games locally:

- **Chain simulator** with integrated WASM execution (same logic as the real chain)
- **3 reference games** — dice, crash, mines (full source + compiled WASM)
- **React UI hooks** for building game frontends
- **Bot framework** that generates live game activity
- **Mock BFF server** (HTTP/SSE) — local dev server you run with one command

For the full protocol explanation — how DKG works, how randomness flows to games, how bankrolls and fees work, and how to design blackjack or parimutuel betting games — read **[CLAUDE_PROMPT.md](CLAUDE_PROMPT.md)**. It's written as context for AI assistants but serves as the complete developer reference.

## Quick Start

```bash
git clone https://github.com/exohash-labs/exohash-devkit
cd exohash-devkit

# Start the local chain + game server
go run ./cmd/mock-bff &

# Start bots (optional — generates live game activity)
go run ./cmd/bot-runner &

# Start the demo UI
cd demo && npm install && npm run dev
```

Open http://localhost:3002 — play dice, crash, mines with live bots. Tap **+ FAUCET** for test tokens.

## Repository Structure

```
CLAUDE_PROMPT.md       Full developer reference — protocol, game patterns, WASM API, dice template
chainsim/              Chain simulator with WASM game execution
gamekit/               WASM game examples (source + compiled)
  examples/dice/       Solo instant game — bet, roll, settle in 2 blocks
  examples/mines/      Solo multi-action — reveal tiles, cashout anytime
  examples/crash/      Session multiplayer — rising multiplier, multiple players
uikit/                 React hooks (useMines, useCrash, useDice, useStream, ...)
bots/                  Bot implementations (dice, crash, mines) — HTTP clients
demo/                  Next.js demo app with all 3 games
cmd/mock-bff/          Local HTTP/SSE dev server
cmd/bot-runner/        Bot runner process
wasm/                  Pre-compiled game binaries
tests/                 Game simulation suite (go run ./tests/dice, ./tests/mines, ./tests/crash)
config.yaml            Mock BFF config (games, bankroll, faucet)
bots.yaml              Bot config (strategies, frequencies)
```

## Build a Game

1. Read [CLAUDE_PROMPT.md](CLAUDE_PROMPT.md) — protocol, game patterns, WASM API, complete dice template
3. Write your game in Go, compile with TinyGo:
   ```bash
   tinygo build -o mygame.wasm -target=wasi -no-debug -opt=2 .
   ```
4. Add to `config.yaml`, restart mock-bff — your game is live locally
5. Deploy to chain: `exohashd tx house register-calculator mygame.wasm`

Or paste `CLAUDE_PROMPT.md` into Claude / ChatGPT and say: *"Build me a blackjack game"*

## Build a React UI

```tsx
import { ExoProvider, useMines } from "@exohash/uikit";

<ExoProvider bffUrl="http://localhost:4000" address={addr}>
  <MinesGame />
</ExoProvider>

function MinesGame() {
  const { start, reveal, cashout, board, active, multiplier } = useMines();
}
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/stream` | SSE event stream (`?games=1,2&address=x`) |
| POST | `/relay/place-bet` | Place a bet |
| POST | `/relay/bet-action` | Player action (reveal, cashout, hit, etc.) |
| POST | `/faucet/request` | Fund test account |
| GET | `/account/{addr}/balance` | Account balance |
| GET | `/account/{addr}/bets` | Bet history |
| GET | `/bet/{id}/state` | Bet state + events (for cold start) |
| GET | `/games` | List registered games |
| GET | `/health` | Server status |

## Links

- [exohash.io](https://exohash.io)
- [Discord](https://t.co/0bYTwICtzp)
- [FAQ](https://exohash.io/faq)

## License

MIT
