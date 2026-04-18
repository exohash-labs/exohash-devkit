package bots

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// MaxTick mirrors the WASM cap (3.5% growth, 100x max ⇒ tick 134).
const crashMaxTick = 134

// crashTickMults precomputes mult_bp per tick using the same iterative formula
// as the WASM (mult * 10350 / 10000, capped at 1_000_000 bp).
var crashTickMults = func() []uint64 {
	out := make([]uint64, crashMaxTick+1)
	out[0] = 10000
	mult := uint64(10000)
	for i := 1; i <= crashMaxTick; i++ {
		next := mult * 10350 / 10000
		if next <= mult {
			next = mult + 1
		}
		if next >= 1_000_000 {
			out[i] = 1_000_000
			mult = 1_000_000
			continue
		}
		out[i] = next
		mult = next
	}
	return out
}()

// tickForMult returns the smallest tick whose mult ≥ targetBp (binary search).
func tickForMult(targetBp uint64) uint64 {
	lo, hi := 1, crashMaxTick
	for lo < hi {
		mid := (lo + hi) / 2
		if crashTickMults[mid] < targetBp {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return uint64(lo)
}

// CrashBot joins every round at fixed stake with WASM-side autocashout. No
// manual cashout: the WASM settles at our target tick deterministically.
type CrashBot struct {
	addr       string
	calcID     uint64
	bankrollID uint64
	stake      uint64
	autoTick   uint64
	betID      uint64
	state      crashState
}

type crashState int

const (
	crashIdle    crashState = iota
	crashJoining            // PlaceBet sent, waiting for "joined" with our addr
	crashActive             // in round, waiting for cashout/settled/crashed
)

type CrashBotConfig struct {
	Address    string
	CalcID     uint64
	BankrollID uint64
	Stake      uint64
	Cashout    uint64 // bp; 0 ⇒ ride to max (tick 134)
}

func NewCrashBot(cfg CrashBotConfig) *CrashBot {
	tick := uint64(crashMaxTick)
	if cfg.Cashout > 0 {
		tick = tickForMult(cfg.Cashout)
	}
	return &CrashBot{
		addr:       cfg.Address,
		calcID:     cfg.CalcID,
		bankrollID: cfg.BankrollID,
		stake:      cfg.Stake,
		autoTick:   tick,
		state:      crashIdle,
	}
}

func (b *CrashBot) Address() string    { return b.addr }
func (b *CrashBot) CalcID() uint64     { return b.calcID }
func (b *CrashBot) BankrollID() uint64 { return b.bankrollID }

func (b *CrashBot) short() string { return b.addr[:8] + ".." + b.addr[len(b.addr)-3:] }

func (b *CrashBot) stateName() string {
	switch b.state {
	case crashIdle:
		return "IDLE"
	case crashJoining:
		return "JOINING"
	case crashActive:
		return "ACTIVE"
	default:
		return "?"
	}
}

func (b *CrashBot) SetBetID(id uint64) {} // legacy no-op

// autoTickBytes encodes b.autoTick as 8-byte LE for PlaceBet gameState.
func (b *CrashBot) autoTickBytes() []byte {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, b.autoTick)
	return buf
}

func (b *CrashBot) OnEvent(topic string, data json.RawMessage) Action {
	switch topic {
	case "joined":
		var d struct {
			BetID uint64 `json:"bet_id"`
			Addr  string `json:"addr"`
		}
		json.Unmarshal(data, &d)
		if d.Addr == b.addr && b.state == crashJoining {
			b.betID = d.BetID
			b.state = crashActive
			fmt.Printf("CRASH %s: JOINING → joined betID=%d → ACTIVE (auto@tick=%d/%.2fx)\n",
				b.short(), b.betID, b.autoTick, float64(crashTickMults[b.autoTick])/10000)
		}

	case "cashout":
		var d struct {
			BetID  uint64 `json:"bet_id"`
			MultBP uint64 `json:"mult_bp"`
			Payout uint64 `json:"payout"`
		}
		json.Unmarshal(data, &d)
		if d.BetID == b.betID && b.state == crashActive {
			fmt.Printf("CRASH %s: ACTIVE → cashout mult=%.2fx payout=%.2f → IDLE\n",
				b.short(), float64(d.MultBP)/10000, float64(d.Payout)/1e6)
			b.state = crashIdle
			b.betID = 0
		}

	case "settled":
		var d struct {
			BetID  uint64 `json:"bet_id"`
			Kind   uint64 `json:"kind"`
			MultBP uint64 `json:"mult_bp"`
			Payout uint64 `json:"payout"`
		}
		json.Unmarshal(data, &d)
		if d.BetID == b.betID && b.state == crashActive {
			outcome := "lost"
			if d.Kind == 1 {
				outcome = fmt.Sprintf("won %.2f", float64(d.Payout)/1e6)
			}
			fmt.Printf("CRASH %s: ACTIVE → settled mult=%.2fx %s → IDLE\n",
				b.short(), float64(d.MultBP)/10000, outcome)
			b.state = crashIdle
			b.betID = 0
		}

	case "state":
		var d struct {
			Phase string `json:"phase"`
			Round uint64 `json:"round"`
		}
		json.Unmarshal(data, &d)
		switch d.Phase {
		case "open":
			if b.state == crashIdle {
				fmt.Printf("CRASH %s: IDLE → open r=%d → JOINING (stake=%.2f, auto@%.2fx)\n",
					b.short(), d.Round, float64(b.stake)/1e6, float64(crashTickMults[b.autoTick])/10000)
				b.state = crashJoining
				return PlaceBet(b.stake, b.autoTickBytes())
			}
		case "crashed":
			// Defensive fallback: if we never saw cashout/settled, reset.
			if b.state == crashActive || b.state == crashJoining {
				fmt.Printf("CRASH %s: %s → crashed r=%d (no settle event seen) → IDLE\n",
					b.short(), b.stateName(), d.Round)
				b.state = crashIdle
				b.betID = 0
			}
		}
	}
	return None()
}
