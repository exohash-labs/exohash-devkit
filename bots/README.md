# bots

Go bot framework — HTTP clients that play ExoHash games via the relay. Consumed by [`cmd/bot-runner/`](../cmd/bot-runner).

## Files

| File | Purpose |
|------|---------|
| `bot.go`    | `Bot` interface + `Action` type (DoNothing / DoPlaceBet / DoBetAction) |
| `client.go` | HTTP client — `/relay/place-bet`, `/relay/bet-action`, `/faucet/request`, `/games` |
| `stream.go` | SSE client with auto-reconnect; filters out replay frames |
| `runner.go` | Dispatches each SSE event to every bot, executes returned actions concurrently |
| `config.go` | YAML loader for `bots.yaml` |
| `dice.go`   | Timer-based: places bet every N blocks at configured chance |
| `crash.go`  | State machine: IDLE → JOINING → ACTIVE → CASHOUT/crash |
| `mines.go`  | Timer-based: starts round, reveals N tiles, then cashouts |

## Write your own bot

Implement `Bot`:

```go
type Bot interface {
    Address() string
    CalcID() uint64
    BankrollID() uint64
    OnEvent(topic string, data json.RawMessage) Action
    SetBetID(betID uint64)
}
```

`OnEvent` is called for every calc event matching your `CalcID()` plus a `"block"` tick
for every SSE frame. Return `PlaceBet(stake, params)`, `BetAction(betID, action)`,
or `None()`.
