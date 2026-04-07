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
	active     bool
	wantJoin   bool
}

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
		wantJoin:   true,
	}
}

func (b *CrashBot) Address() string { return b.addr }
func (b *CrashBot) CalcID() uint64  { return b.calcID }

func (b *CrashBot) SetBetID(id uint64) {
	b.betID = id
	b.active = true
	b.wantJoin = false
}

func (b *CrashBot) OnEvent(topic string, data json.RawMessage) Action {
	switch topic {
	case "state":
		var d struct {
			Phase  string `json:"phase"`
			MultBP uint64 `json:"mult_bp"`
		}
		json.Unmarshal(data, &d)

		switch d.Phase {
		case "open":
			if !b.active && !b.wantJoin {
				b.betID = 0
				b.wantJoin = true
			}
		case "tick":
			if b.active && b.targetMult > 0 && d.MultBP >= b.targetMult {
				fmt.Printf("BOT %s: CASHOUT at %d betID=%d\n", b.addr, d.MultBP, b.betID)
				return BetAction(b.betID, []byte{1})
			}
		case "crashed":
			b.active = false
		}
		return None()

	case "block":
		if b.wantJoin && !b.active {
			return PlaceBet(b.stake, nil)
		}
		return None()

	default:
		return None()
	}
}
