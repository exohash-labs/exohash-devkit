package bots

import (
	"encoding/json"
	"fmt"
)

// CrashBot joins every round at fixed stake, cashes out at target multiplier.
type CrashBot struct {
	addr       string
	calcID     uint64
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
	Address string
	CalcID  uint64
	Stake   uint64
	Cashout uint64 // bp, 0 = never
}

func NewCrashBot(cfg CrashBotConfig) *CrashBot {
	return &CrashBot{
		addr:       cfg.Address,
		calcID:     cfg.CalcID,
		stake:      cfg.Stake,
		targetMult: cfg.Cashout,
		state:      crashIdle,
	}
}

func (b *CrashBot) Address() string { return b.addr }
func (b *CrashBot) CalcID() uint64  { return b.calcID }

func (b *CrashBot) SetBetID(id uint64) {
	b.betID = id
	b.state = crashActive
}

func (b *CrashBot) OnEvent(topic string, data json.RawMessage) Action {
	if topic != "state" {
		return None()
	}

	var d struct {
		Phase  string `json:"phase"`
		MultBP uint64 `json:"mult_bp"`
	}
	json.Unmarshal(data, &d)

	switch d.Phase {
	case "open":
		if b.state == crashIdle {
			b.state = crashJoining
			return PlaceBet(b.stake, nil)
		}
	case "tick":
		if b.state == crashActive && b.targetMult > 0 && d.MultBP >= b.targetMult {
			fmt.Printf("BOT %s: CASHOUT at %d betID=%d\n", b.addr, d.MultBP, b.betID)
			b.state = crashCashed
			return BetAction(b.betID, []byte{1})
		}
	case "crashed":
		b.state = crashIdle
		b.betID = 0
	}
	return None()
}
