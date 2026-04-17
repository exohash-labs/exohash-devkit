# ExoHash Developer Guide — Context for AI Assistants

You are helping a developer build games or integrations for the ExoHash protocol. This document explains the full system so you can answer questions, design games, and write working code.

## What is ExoHash

ExoHash is a Cosmos L1 blockchain purpose-built for provably fair gaming. Three properties make it unique:

1. **Unbiasable on-chain randomness** — validators produce a BLS threshold signature every block. The signature is deterministic (same message + same key = same output). No single validator, proposer, or operator can predict or manipulate it.

2. **Permissionless bankrolls** — anyone creates a USDC liquidity pool, attaches games, and earns house edge. No license, no operator, no middleman. LPs deposit and earn proportional to their share.

3. **On-chain WASM game engines** — game logic runs as WebAssembly modules inside the chain. One transaction deploys a game. Any bankroll can list it. Outcomes are deterministic and publicly verifiable.

## How randomness works

### The problem with other approaches
- **Server-generated RNG**: the operator sees the result first and can cheat
- **Commit-reveal**: the last revealer can choose not to reveal, biasing the outcome
- **External oracles**: single point of failure, trust assumption

### ExoHash's solution: BLS threshold signatures

Every ~24 hours, validators run a **Distributed Key Generation (DKG)** protocol:
1. Each validator generates a secret polynomial and publishes commitments
2. Validators exchange encrypted shares of their secrets
3. Each validator combines received shares to derive their BLS key share
4. The chain verifies everything and derives the group public key

No single validator knows the full private key. To produce any valid signature, at least 2/3 of validators must participate.

**Every block**, the beacon produces randomness:
1. Each validator signs the message `"EXO_RAND_V1|{chain_id}|{epoch}|{height}"` with their BLS key share
2. The block proposer aggregates the first T shares using Lagrange interpolation
3. The result is a unique 96-byte BLS signature — always the same for the same inputs
4. `randomness = SHA256(aggregate_signature)` → 32-byte seed stored on-chain

**Why this can't be cheated:**
- BLS threshold signatures are **unique** — given the same message and key, there is exactly one valid signature
- The proposer can't choose a different signature — they can only aggregate honestly or produce nothing
- Withholding shares = no randomness = no block (liveness incentive forces participation)
- The randomness is available one block later (games placed at height H get randomness from height H at block H+1)

### The N+1 rule — why nobody can cheat

This is the most critical security property of the entire system.

**A bet placed at block N is resolved using randomness from block N, which is only available at block N+1.**

```
Block N:   Player submits MsgPlaceBet → bet created, funds escrowed
           Randomness for block N is being produced RIGHT NOW by validators
           But the game CANNOT access it yet — get_rng(N) is blocked

Block N+1: block_update runs → game calls get_rng(N) → gets the seed
           Game computes outcome → settles the bet
```

Why this matters:
- The **proposer** of block N sees the player's bet in the mempool. If they could use block N's randomness immediately, they could front-run: include the bet only if it loses.
- By delaying RNG access by one block, the proposer commits to including the bet BEFORE the randomness is known. The randomness for block N is finalized by block N's validators, but the game can't read it until block N+1.
- The **player** also can't time their bet to exploit known randomness — by the time randomness at height H is readable, height H+1 has already started.

**In your WASM code:** `get_rng(height)` returns 0 (failure) if `height >= current_block_height`. Always request `get_rng(height - 1)` from inside `block_update(height)`.

### How games consume randomness

Games call `get_rng(height - 1)` during `block_update(height)`. This returns the 32-byte beacon seed. The game derives per-bet randomness:

```
per_bet_seed = SHA256(beacon_seed || bet_id)
roll = uint64(per_bet_seed[0:8]) % range
```

This ensures: same block, different bets → different outcomes. Same bet replayed → same outcome.

## The economic loop

```
Game Developers  → build WASM games, deploy on-chain (one tx)
Bankroll Creators → create USDC pools, list games, set parameters
Liquidity Providers → deposit USDC into bankrolls, earn house edge
Players → bet against bankrolls with provably fair outcomes
Validators → run DKG, produce randomness, earn 30% of house edge fees
EXOH Stakers → govern the protocol, earn 70% of house edge fees
```

### Fee split on every bet

```
Player stakes 100 USDC on a game with 2% house edge:

  Edge = 100 × 2% = 2.00 USDC
  Protocol take rate = 25% of edge = 0.50 USDC
    → 30% to validators (0.15 USDC)
    → 70% to EXOH stakers (0.35 USDC)
  Bankroll net = 100 - 0.50 = 99.50 USDC (risked against the player)
```

The bankroll earns on aggregate — players lose 2% on average. LPs earn that edge minus the protocol fee.

## Bankroll mechanics

A **bankroll** is a permissionless USDC liquidity pool:

- **Create**: anyone deposits initial USDC → receives LP shares
- **Deposit**: others deposit → receive shares proportional to pool size
- **Attach games**: bankroll owner enables specific WASM calculators
- **Bets**: players bet against the pool. Max payout capped at `max_payout_bps` % of pool (default 2%). Total reserved capped at `max_reserved_bps` % (default 80%).
- **Withdraw**: LP redeems shares → receives proportional USDC (after queue delay)

Multiple bankrolls can exist simultaneously with different:
- Game selections
- Max payout caps
- Pool sizes
- Fee configurations

This creates a **market for liquidity** — bankrolls compete for players and LPs.

## Game architecture

A game is a WASM binary (~30-50KB) with these exports:

| Export | When called | Purpose |
|--------|------------|---------|
| `init_game(sentinel_id, bankroll_id, calc_id)` | Once on setup | Initialize game state, start tick loops |
| `place_bet(bet_id, bankroll_id, calc_id, stake, params_ptr, params_len) → u32` | Per bet | Validate, reserve funds, schedule wakeup |
| `bet_action(bet_id, action_ptr, action_len) → u32` | Per player action | Cashout, reveal, hit/stand, etc. |
| `block_update(height)` | Each block | Resolve RNG, settle bets, tick game loops |
| `info() → ptr` | On registration | Game metadata JSON |
| `query() → ptr` | On demand | Current game state JSON |
| `alloc(size) → ptr` | Memory mgmt | Allocate WASM linear memory |

Return 0 = success, non-zero = error code.

### Host functions (WASM imports from "env")

The chain provides these functions to every WASM game:

**State storage** — persistent key-value store per game
```
kv_get(key_ptr, key_len) → u64         — read state (returns packed ptr|len)
kv_set(key_ptr, key_len, val_ptr, val_len) — write state
kv_has(key_ptr, key_len) → u32         — check key exists
```

**Scheduling** — request callbacks at future blocks
```
schedule_wakeup(bet_id, height)   — call block_update at height (0 = next block)
cancel_wakeup(bet_id)             — remove scheduled callback
```

**Financial** — move money (the chain handles actual transfers)
```
reserve(bet_id, amount) → u32         — lock bankroll funds for potential payout
settle(bet_id, payout, kind) → u32    — resolve bet: kind 1=win, 2=loss, 3=refund
increase_stake(bet_id, amount) → u32  — take more from player (blackjack double down)
```

**Randomness** — beacon-derived seed
```
get_rng(height, out_ptr) → u32   — write 32-byte seed (only available in block_update)
```

**Data** — read bet context during block_update
```
get_bet_count() → u32           — how many bets woke up this block
get_bet_id(index) → u64         — get bet_id by wakeup index
get_pending_action(bet_id) → u32 — read queued player action
get_bettor(bet_id) → u32        — get player address
```

**Events** — emit JSON for UIs and indexers
```
emit_event(topic_ptr, topic_len, data_ptr, data_len)
```

### Key rules

1. **Params format**: `place_bet` receives `sender(20 bytes) + game_params`. First 20 bytes = player address (injected by chain). Your params start at byte 20.

2. **Key by bet ID**: For concurrent games, prefix KV keys with bet ID: `e/{betID}/state`. Never use global keys — multiple bets run simultaneously.

3. **Per-bet RNG**: Always mix bet ID into the seed: `SHA256(rng_seed || bet_id)`. Otherwise all bets settled in the same block get the same outcome.

4. **settle() handles money**: You don't transfer tokens. Call `settle(betID, payout, kind)` and the chain moves funds.

5. **Block lifecycle**: `block_update` runs FIRST in each block, then user transactions. A bet placed at block N gets its first `block_update` at block N+1.

6. **N+1 RNG rule**: `get_rng(H)` is only available during `block_update` at height H+1 or later. Never during `place_bet` or `bet_action`. This is the core anti-cheat mechanism — see "The N+1 rule" section above. In your `block_update(height)`, always call `get_rng(height - 1)`.

## Game lifecycle patterns

### Pattern 1: Solo instant (dice, coinflip, plinko, limbo, wheel)

```
place_bet → reserve(max_payout) → schedule_wakeup(betID, 0)
block_update → get_rng → compute result → settle
```

Two blocks total: place + settle. Simplest pattern.

**Example — Coin flip:**
- Player bets 1 USDC, 50/50 chance, 1.96x payout (2% edge)
- `place_bet`: reserve 1.96 USDC from bankroll, schedule wakeup
- `block_update`: get RNG, derive flip, settle as win (1.96) or loss (0)

### Pattern 2: Solo multi-action (mines, blackjack, keno, video poker)

```
place_bet → reserve(max_payout), no wakeup yet
bet_action(reveal/hit) → store action → schedule_wakeup
block_update → get_rng → resolve action → update state or settle
bet_action(cashout) → settle immediately (no RNG needed)
```

Player takes multiple actions, each requiring RNG. Includes timeout protection (auto-settle after N blocks of inactivity).

**Example — Blackjack:**
- `place_bet`: reserve 2x stake (for blackjack payout). Store initial deal request.
- `block_update`: deal 2 cards to player, 1 to dealer (from RNG). Emit "dealt" event.
- `bet_action([HIT])`: player wants another card → store pending action → schedule wakeup
- `block_update`: draw card from RNG. If bust → settle loss. If not → wait for next action.
- `bet_action([STAND])`: player stands → schedule wakeup for dealer play
- `block_update`: dealer draws until 17+ → compare hands → settle
- `bet_action([DOUBLE])`: `increase_stake(betID, original_stake)` → draw one card → settle
- Timeout: if player doesn't act for 40 blocks → auto-stand

### Pattern 3: Session multiplayer (crash, roulette, boxing/sports parimutuel)

```
init_game → create round state → schedule_wakeup (starts tick loop)
place_bet → join round, reserve max payout
bet_action(cashout/claim) → queue for next tick
block_update → tick: advance game, process cashouts, settle → schedule next tick
After round ends → auto-start new round
```

Perpetual tick loop. Multiple players per round. One shared outcome.

**Example — Virtual boxing parimutuel:**
- `init_game`: create first fight, set open window (16 blocks)
- Round phases: BETTING (16 blocks) → FIGHT (20 blocks with tick events) → SETTLED (5 block cooldown)
- `place_bet`: player bets on Fighter A or Fighter B during BETTING phase
- `block_update` during FIGHT: emit round-by-round commentary events (derived from RNG). At final tick, determine winner.
- Settlement: total pot split among winners proportional to their bets, minus house edge.
- Pool math: `payout = (my_bet / winning_side_total) × total_pot × (1 - edge)`

## House edge math

The `house_edge_bp` (basis points, 200 = 2%) reduces the player's expected return:

```go
// Dice/coinflip: reduce effective chance
fairMult := 10000 * 10000 / chance    // e.g. 50% chance → 2.0x
edgedMult := fairMult * (10000 - edgeBP) / 10000  // → 1.96x

// Mines: reduce multiplier per reveal
fairMultBP := 10000 * totalTiles / (totalTiles - minesLeft)
edgedMultBP := fairMultBP * (10000 - edgeBP) / 10000

// Crash: house edge reduces survival probability
survivalProb := (1 - edge/10000) × (currentMult / nextMult)
// Crash point derived from RNG: crashMult = 10000 / (1 - rand) × (1 - edge/10000)

// Parimutuel: edge taken from total pot before distribution
payoutPool := totalBets × (10000 - edgeBP) / 10000
```

## This repository (exohash-devkit)

```
chainsim/              Chain simulator — runs WASM games locally (same logic as real chain)
games/                 Reference games: source + compiled WASM + test suite per game
  dice/                Solo instant game (~36KB WASM)
  mines/               Solo multi-action game (~33KB WASM)
  crash/               Session multiplayer game (~38KB WASM)
cmd/bffsim/            Mock HTTP/SSE BFF on :4000 wrapping chainsim
cmd/bot-runner/        Bot runner process
bots/                  Bot framework — HTTP clients that play games
ui/                    Next.js reference casino frontend (exohash-play snapshot)
```

### Running locally
Game-only iteration (no node) — run the chain simulator test suite per game:
```bash
git clone https://github.com/exohash-labs/exohash-devkit
cd exohash-devkit/games/dice && go run .    # house edge + error semantics + gas
```

Frontend dev (mock backend, no node):
```bash
go run ./cmd/bffsim                                    # terminal 1 — :4000
cd ui && npm install && npm run build && npm start -- --port 3001   # terminal 2 — :3001
go run ./cmd/bot-runner                                # terminal 3 — 15 bots (optional)
```

End-to-end dev stack (real node + BFF + bots + UI) lives in the main
[exohash](https://github.com/exohash-labs/exohash) repo — run `scripts/run_all.sh`.

### Building a new WASM game
```bash
# Write your game in Go (see games/dice/ for the reference template)
mkdir mygame && cd mygame
# ... write main.go with exports: place_bet, block_update, info, alloc ...
tinygo build -o mygame.wasm -target=wasi -no-debug -opt=2 .

# Test against chainsim first (add a sim harness under tests/mygame),
# then deploy to the chain (see below).
```

### Deploying to the real chain
```bash
exohashd tx house register-calculator mygame.wasm --from developer
exohashd tx house bankroll-add-calculator 1 <calc_id> --from bankroll_owner
# Done. Players can bet.
```

## Building a React UI

The `ui/` directory is a full Next.js casino frontend — a snapshot of
[`exohash-play`](https://github.com/exohash-labs/exohash-play). Use it as a runnable
demo, a reference implementation, or a fork target.

Key pieces to copy when building your own:

- `ui/lib/bff.ts`      — REST client for the BFF (bets, faucet, accounts, games)
- `ui/lib/signer.ts`   — cosmjs-based authz grant signing
- `ui/lib/wallet.ts`   — browser HD wallet (create/import/unlock/lock, encrypted storage)
- `ui/lib/useStream.ts` — SSE subscription with replay → live boundary via `flushSync`
- `ui/lib/useBetFeed.ts` — generic calc-event feed with a per-game parser
- `ui/lib/useWaitForBet.ts` — watch a single bet to settlement
- `ui/contexts/`        — React context providers wiring the above together

For dev you only need `bffsim` — the Cosmos REST mocks let the signer path run
unchanged against it, so the same UI code works against bffsim or a real chain.

## What to build

### Beginner
- **Coin flip** — 50/50, 1.96x. Simplest possible game (solo instant pattern)
- **Wheel of fortune** — weighted segments, single spin (solo instant)
- **Hi-lo** — draw a card, guess higher or lower (solo instant)

### Intermediate
- **Blackjack** — hit/stand/double/split, dealer rules (solo multi-action)
- **Video poker** — 5 cards, hold/discard, payout table (solo multi-action)
- **Keno** — pick numbers, draw from pool (solo multi-action)

### Advanced
- **Roulette** — multiple bet types, single spin per round (session multiplayer)
- **Virtual boxing/racing** — parimutuel betting, round-by-round events (session multiplayer)
- **Lottery** — ticket purchase phase, single draw, split pot (session multiplayer)

### Key design considerations
- Every game state mutation must go through KV store (persistence across blocks)
- RNG is only available in `block_update`, never in `place_bet` or `bet_action`
- Always include timeout protection: if player doesn't act for N blocks, auto-settle
- Emit events for every meaningful state change — UIs depend on them
- Test house edge convergence: run 100K+ bets and verify the edge matches your target

## Suggested prompts for this AI

When working with a developer, suggest:
1. *"Run the dice example and explain the block lifecycle"*
2. *"Build me a coin flip game"* — verifies they understand the simplest pattern
3. *"Build a blackjack game with hit, stand, and double"* — tests multi-action flow
4. *"Build a virtual boxing parimutuel game"* — tests session multiplayer with shared outcomes
5. *"Run a house edge simulation for my game at 100K bets"* — validates the math

## Complete reference: dice game

This is a complete, working dice game. Use it as a template for any solo instant game.

```go
package main

import (
	"encoding/binary"
	"unsafe"
)

// --- Host imports ---
//go:wasmimport env kv_get
func kv_get(keyPtr, keyLen uint32) uint64
//go:wasmimport env kv_set
func kv_set(keyPtr, keyLen, valPtr, valLen uint32)
//go:wasmimport env schedule_wakeup
func schedule_wakeup(betID, height uint64)
//go:wasmimport env reserve
func host_reserve(betID, amount uint64) uint32
//go:wasmimport env settle
func host_settle(betID, payout uint64, kind uint32) uint32
//go:wasmimport env get_rng
func host_get_rng(height uint64, outPtr uint32) uint32
//go:wasmimport env get_bet_count
func host_get_bet_count() uint32
//go:wasmimport env get_bet_id
func host_get_bet_id(index uint32) uint64
//go:wasmimport env emit_event
func host_emit_event(topicPtr, topicLen, dataPtr, dataLen uint32)

// --- Memory exports ---
//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}
//export dealloc
func dealloc(ptr *byte, size uint32) {}

// --- Constants ---
const (
	houseEdgeBP = 200 // 2%
	kindWin     = 1
	kindLoss    = 2
)

// --- place_bet ---
//export place_bet
func place_bet(betID, bankrollID, calculatorID, stake uint64, paramsPtr, paramsLen uint32) uint32 {
	params := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(paramsPtr))), paramsLen)
	if len(params) < 29 {
		return 1 // invalid params
	}
	// sender at params[0:20] — not used by dice
	betMode := params[20]
	threshold := binary.LittleEndian.Uint64(params[21:29])

	chance := chanceBP(betMode, threshold)
	if chance < 100 || chance > 9800 {
		return 2 // chance out of range
	}
	maxPayout := stake * fairMultBP(chance) / 10000
	if host_reserve(betID, maxPayout) != 0 {
		return 3 // insufficient liquidity
	}

	// Store state keyed by betID
	state := make([]byte, 25)
	binary.LittleEndian.PutUint64(state[0:], betID)
	binary.LittleEndian.PutUint64(state[8:], stake)
	state[16] = betMode
	binary.LittleEndian.PutUint64(state[17:], threshold)
	kvSet(betKey(betID), state)

	schedule_wakeup(betID, 0) // resolve next block
	emitJSON("bet", "entry_id", betID, "stake", stake, "chance_bp", chance)
	return 0
}

// --- block_update: resolve all pending bets ---
//export block_update
func block_update(height uint64) {
	count := host_get_bet_count()
	for i := uint32(0); i < count; i++ {
		betID := host_get_bet_id(i)
		state := kvGetBytes(betKey(betID))
		if state == nil || len(state) < 25 {
			continue
		}
		storedID := binary.LittleEndian.Uint64(state[0:8])
		stake := binary.LittleEndian.Uint64(state[8:16])
		betMode := state[16]
		threshold := binary.LittleEndian.Uint64(state[17:25])

		// N+1 rule: get randomness from the PREVIOUS block
		rngBuf := make([]byte, 32)
		if host_get_rng(height-1, uint32(uintptr(unsafe.Pointer(&rngBuf[0])))) == 0 {
			schedule_wakeup(betID, height+1) // retry next block
			continue
		}

		chance := chanceBP(betMode, threshold)
		mult := fairMultBP(chance)
		effChance := chance * (10000 - houseEdgeBP) / 10000
		roll := deriveRoll(rngBuf, storedID)
		win := isWin(betMode, roll, effChance)

		payout, kind := uint64(0), uint32(kindLoss)
		if win {
			payout = stake * mult / 10000
			kind = uint32(kindWin)
		}
		host_settle(betID, payout, kind)

		result := "loss"
		if win { result = "win" }
		emitJSON("settle", "entry_id", storedID, "roll", roll, "payout", payout, "result", result)
	}
}

// --- info ---
//export info
func info() *byte {
	data := []byte(`{"name":"Dice","engine":"dice","mode":"v3","house_edge_bp":200}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
}

// --- Game math ---
func chanceBP(mode byte, threshold uint64) uint64 {
	if mode == 1 { return 10000 - threshold } // under
	if mode == 2 { return threshold }          // over
	return 0
}
func fairMultBP(chance uint64) uint64 {
	if chance == 0 { return 0 }
	return (10000 * 10000) / chance
}
func isWin(mode byte, roll, effChance uint64) bool {
	if mode == 1 { return roll >= (10000 - effChance) }
	if mode == 2 { return roll < effChance }
	return false
}
func deriveRoll(seed []byte, entryID uint64) uint64 {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], entryID)
	data := make([]byte, len(seed)+8)
	copy(data, seed)
	copy(data[len(seed):], buf[:])
	sum := sha256sum(data)
	return binary.BigEndian.Uint64(sum[0:8]) % 10000
}

// --- KV helpers ---
func betKey(betID uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'b'
	binary.LittleEndian.PutUint64(buf[1:], betID)
	return buf
}
func kvSet(key, value []byte) {
	kv_set(uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)),
		uint32(uintptr(unsafe.Pointer(&value[0]))), uint32(len(value)))
}
func kvGetBytes(key []byte) []byte {
	packed := kv_get(uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)))
	if packed == 0 { return nil }
	ptr := uint32(packed >> 32)
	length := uint32(packed & 0xFFFFFFFF)
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

// --- Event helpers ---
func emitJSON(topic string, pairs ...interface{}) {
	json := fmtJSON(pairs...)
	t := []byte(topic)
	j := []byte(json)
	host_emit_event(uint32(uintptr(unsafe.Pointer(&t[0]))), uint32(len(t)),
		uint32(uintptr(unsafe.Pointer(&j[0]))), uint32(len(j)))
}
func fmtJSON(pairs ...interface{}) string {
	buf := make([]byte, 0, 128)
	buf = append(buf, '{')
	for i := 0; i < len(pairs)-1; i += 2 {
		if i > 0 { buf = append(buf, ',') }
		key := pairs[i].(string)
		buf = append(buf, '"')
		buf = append(buf, key...)
		buf = append(buf, '"', ':')
		switch v := pairs[i+1].(type) {
		case uint64: buf = appendUint(buf, v)
		case string: buf = append(buf, '"'); buf = append(buf, v...); buf = append(buf, '"')
		}
	}
	buf = append(buf, '}')
	return string(buf)
}
func appendUint(buf []byte, v uint64) []byte {
	if v == 0 { return append(buf, '0') }
	var tmp [20]byte
	i := len(tmp)
	for v > 0 { i--; tmp[i] = byte('0' + v%10); v /= 10 }
	return append(buf, tmp[i:]...)
}

// --- SHA-256 (inline — crypto/sha256 panics in TinyGo WASM) ---
var sha256K = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}
func sha256sum(data []byte) [32]byte {
	h0, h1, h2, h3 := uint32(0x6a09e667), uint32(0xbb67ae85), uint32(0x3c6ef372), uint32(0xa54ff53a)
	h4, h5, h6, h7 := uint32(0x510e527f), uint32(0x9b05688c), uint32(0x1f83d9ab), uint32(0x5be0cd19)
	msgLen := len(data)
	bitLen := uint64(msgLen) * 8
	data = append(data, 0x80)
	for len(data)%64 != 56 { data = append(data, 0) }
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], bitLen)
	data = append(data, lenBuf[:]...)
	var w [64]uint32
	for off := 0; off < len(data); off += 64 {
		block := data[off : off+64]
		for i := 0; i < 16; i++ { w[i] = binary.BigEndian.Uint32(block[i*4:]) }
		for i := 16; i < 64; i++ {
			s0 := (w[i-15]>>7|w[i-15]<<25) ^ (w[i-15]>>18|w[i-15]<<14) ^ (w[i-15]>>3)
			s1 := (w[i-2]>>17|w[i-2]<<15) ^ (w[i-2]>>19|w[i-2]<<13) ^ (w[i-2]>>10)
			w[i] = w[i-16] + s0 + w[i-7] + s1
		}
		a, b, c, d, e, f, g, h := h0, h1, h2, h3, h4, h5, h6, h7
		for i := 0; i < 64; i++ {
			S1 := (e>>6|e<<26) ^ (e>>11|e<<21) ^ (e>>25|e<<7)
			ch := (e & f) ^ (^e & g)
			t1 := h + S1 + ch + sha256K[i] + w[i]
			S0 := (a>>2|a<<30) ^ (a>>13|a<<19) ^ (a>>22|a<<10)
			maj := (a & b) ^ (a & c) ^ (b & c)
			t2 := S0 + maj
			h = g; g = f; f = e; e = d + t1; d = c; c = b; b = a; a = t1 + t2
		}
		h0 += a; h1 += b; h2 += c; h3 += d; h4 += e; h5 += f; h6 += g; h7 += h
	}
	var out [32]byte
	binary.BigEndian.PutUint32(out[0:], h0); binary.BigEndian.PutUint32(out[4:], h1)
	binary.BigEndian.PutUint32(out[8:], h2); binary.BigEndian.PutUint32(out[12:], h3)
	binary.BigEndian.PutUint32(out[16:], h4); binary.BigEndian.PutUint32(out[20:], h5)
	binary.BigEndian.PutUint32(out[24:], h6); binary.BigEndian.PutUint32(out[28:], h7)
	return out
}

func main() {}
```

Note: `crypto/sha256` panics in TinyGo WASM, so the SHA-256 implementation is inlined. Copy the `sha256sum`, `sha256K`, `kvSet`, `kvGetBytes`, `emitJSON`, `fmtJSON`, and `alloc` functions into every game — they're boilerplate.
