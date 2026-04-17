# bot-runner

Spawns 15 HTTP-client bots (configurable) that place live bets across the three reference games. Useful for populating the UI's live-bet feed during development.

## Run

```bash
# against bffsim (default)
go run ./cmd/bot-runner

# against real chain's BFF — addresses must have keyring entries + authz grants
BOT_ADDRS="exo1...,exo1...,..." go run ./cmd/bot-runner
```

When `BOT_ADDRS` is **unset**, fresh bech32 addresses are generated and funded via the faucet. This is the bffsim path — the faucet and authz-grant endpoints are both mocked.

When `BOT_ADDRS` is **set**, each comma-separated address is used as-is. This is the real-chain path — each address must exist on the chain keyring and have granted authz to the BFF relay key.

## Configuration

[`bots.yaml`](../../bots.yaml) at the repo root:

```yaml
bffUrl: http://localhost:4000

crash:
  - { name: crash-safe,  stake: 1000000, cashout: 12000 }   # target 1.2x
  - { name: crash-mid,   stake: 1000000, cashout: 15000 }   # target 1.5x
  # …

dice:
  - { name: dice-safe,   stake: 500000,  chanceBp: 7500, every: 6 }
  - { name: dice-mid,    stake: 1000000, chanceBp: 5000, every: 8 }
  # …

mines:
  - { name: mines-careful, stake: 1000000, mines: 3, reveals: 2, every: 20 }
  # …
```

- `stake` — per-bet stake in uusdc
- `cashout` — crash cashout multiplier in bp (12000 = 1.2x)
- `chanceBp` — dice win chance in bp
- `mines` — number of mines
- `reveals` — number of tiles to reveal before cashout
- `every` — bot fires every N SSE events (roughly N blocks)

Bots count must equal total entries (crash + dice + mines). If you add entries, bot-runner generates more addresses automatically when `BOT_ADDRS` is unset.

## Architecture

```
SSE /stream → bots/stream.go → channel → Runner.ProcessEvent
                                              │
              ┌───────────────────────────────┤
              ▼                               ▼
        bot.OnEvent("block")            bot.OnEvent(calcTopic)
              │                               │
              └─────── Action{PlaceBet|BetAction|None} ─────► HTTP relay
```

Each bot implements the `Bot` interface in `bots/bot.go`. Strategies are in
`bots/dice.go`, `bots/crash.go`, `bots/mines.go`.
