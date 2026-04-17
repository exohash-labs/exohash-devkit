package bots

import (
	"encoding/json"
	"fmt"
)

// CrashBot joins every round at fixed stake, cashes out at target multiplier.
type CrashBot struct {
	addr       string
	calcID     uint64
	bankrollID uint64
	stake      uint64
	targetMult uint64
	betID      uint64
	state      crashState // idle → joining → active → cashed → idle
}

type crashState int

const (
	crashIdle    crashState = iota
	crashJoining            // PlaceBet sent, waiting for SetBetID
	crashActive             // in round, watching multiplier
	crashCashed             // cashout sent, waiting for round end
)

type CrashBotConfig struct {
	Address    string
	CalcID     uint64
	BankrollID uint64
	Stake      uint64
	Cashout uint64 // bp, 0 = never
}

func NewCrashBot(cfg CrashBotConfig) *CrashBot {
	return &CrashBot{
		addr:       cfg.Address,
		calcID:     cfg.CalcID,
		bankrollID: cfg.BankrollID,
		stake:      cfg.Stake,
		targetMult: cfg.Cashout,
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
	case crashCashed:
		return "CASHED"
	default:
		return "?"
	}
}

func (b *CrashBot) SetBetID(id uint64) {
	// No longer used — betID comes from SSE "joined" event
}

func (b *CrashBot) OnEvent(topic string, data json.RawMessage) Action {
	// Pick up betID from SSE "joined" event matching our address
	if topic == "joined" {
		var d struct {
			BetID uint64 `json:"bet_id"`
			Addr  string `json:"addr"`
		}
		json.Unmarshal(data, &d)
		if d.Addr == b.addr && b.state == crashJoining {
			b.betID = d.BetID
			b.state = crashActive
			fmt.Printf("CRASH %s: JOINING → joined betID=%d → ACTIVE\n", b.short(), b.betID)
		}
		return None()
	}
	if topic != "state" {
		return None()
	}

	var d struct {
		Phase  string `json:"phase"`
		MultBP uint64 `json:"mult_bp"`
		Round  uint64 `json:"round"`
	}
	json.Unmarshal(data, &d)

	switch d.Phase {
	case "open":
		if b.state == crashIdle {
			fmt.Printf("CRASH %s: IDLE → open r=%d → JOINING (stake=%.2f)\n", b.short(), d.Round, float64(b.stake)/1e6)
			b.state = crashJoining
			return PlaceBet(b.stake, nil)
		}
	case "tick":
		if b.state == crashActive && b.targetMult > 0 && d.MultBP >= b.targetMult {
			fmt.Printf("CRASH %s: ACTIVE → tick mult=%d >= target=%d → CASHOUT betID=%d\n", b.short(), d.MultBP, b.targetMult, b.betID)
			b.state = crashCashed
			return BetAction(b.betID, []byte{1})
		}
	case "crashed":
		prev := b.stateName()
		wasBusted := b.state == crashActive
		b.state = crashIdle
		b.betID = 0
		if wasBusted {
			fmt.Printf("CRASH %s: %s → crashed r=%d mult=%d → IDLE (busted)\n", b.short(), prev, d.Round, d.MultBP)
		} else {
			fmt.Printf("CRASH %s: %s → crashed r=%d → IDLE\n", b.short(), prev, d.Round)
		}
	}
	return None()
}
