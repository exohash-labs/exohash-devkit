# games

Three reference games, each a self-contained Go module with:

- **`main.go`** — WASM game source code (compiled to `*.wasm`)
- **`<name>.wasm`** — pre-compiled TinyGo bytecode, deployed by `cmd/bffsim` at startup
- **`README.md`** — auto-generated test report (house edge, error semantics, gas accounting)
- **`TESTS.md`** — per-game test reference (where present)

Wait — that's slightly wrong. The test suite lives in `main.go` but `main.go` also generates
the README. To avoid conflating sources, each game is actually structured as:

```
games/dice/
  main.go        Test suite (generates README.md) — runs chainsim with the WASM
  dice.wasm      Pre-built WASM bytecode
  README.md      Test report (do not hand-edit — regenerate with `go run .`)
  src/           (optional) WASM source code in Go — compiled to dice.wasm
```

Run the test suite:

```bash
cd games/dice && go run .
cd games/mines && go run .
cd games/crash && go run .
```

Each run:
1. Loads the WASM into chainsim
2. Runs three sections — house-edge convergence, wrong-player/error behavior, gas accounting
3. Prints a live report to stdout
4. Regenerates `README.md` in the game directory
5. Writes `results.txt` for CI (gitignored)

## Patterns by game

| Game  | Type | Settles | Actions |
|-------|------|---------|---------|
| dice  | solo instant | next block | `place_bet` only |
| mines | solo multi-step | on cashout or mine hit | `place_bet` + `reveal` + `cashout` |
| crash | multiplayer session | on round crash | `place_bet` (join round) + `cashout` |

Use these as templates for your own game — see [`CLAUDE_PROMPT.md`](../CLAUDE_PROMPT.md) for
the WASM API (`place_bet`, `bet_action`, `block_update`, `info`, `alloc`) and the complete
dice walkthrough.
