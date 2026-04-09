package bots

import (
	"encoding/json"
	"math/rand"
)

// Fixed tile reveal order — avoids "already revealed" errors.
var revealOrder = []byte{2, 1, 16, 15, 11}

// MinesBot plays mines — starts a game, reveals tiles, cashes out.
type MinesBot struct {
	addr          string
	calcID        uint64
	minStake      uint64
	maxStake      uint64
	minesCount    int
	maxReveals    int
	betID         uint64
	revealed      int
	active        bool
	waiting       bool
	pendingReveal bool
	every         int
	counter       int
	rng           *rand.Rand
}

type MinesBotConfig struct {
	Address    string
	CalcID     uint64
	MinStake   uint64
	MaxStake   uint64
	MinesCount int
	MaxReveals int
	Every      int
	Seed       int64
}

func NewMinesBot(cfg MinesBotConfig) *MinesBot {
	every := cfg.Every
	if every <= 0 {
		every = 20
	}
	mines := cfg.MinesCount
	if mines <= 0 {
		mines = 3
	}
	reveals := cfg.MaxReveals
	if reveals <= 0 {
		reveals = 2
	}
	return &MinesBot{
		addr:       cfg.Address,
		calcID:     cfg.CalcID,
		minStake:   cfg.MinStake,
		maxStake:   cfg.MaxStake,
		minesCount: mines,
		maxReveals: reveals,
		every:      every,
		rng:        rand.New(rand.NewSource(cfg.Seed)),
	}
}

func (b *MinesBot) Address() string { return b.addr }
func (b *MinesBot) CalcID() uint64  { return b.calcID }

func (b *MinesBot) SetBetID(id uint64) {
	b.betID = id
	b.active = true
	b.revealed = 0
	b.waiting = true
	b.pendingReveal = true
}

func (b *MinesBot) OnEvent(topic string, data json.RawMessage) Action {
	switch topic {
	case "reveal":
		var d struct {
			BetID uint64 `json:"bet_id"`
			Safe  uint64 `json:"safe"`
		}
		json.Unmarshal(data, &d)
		if d.BetID != b.betID || !b.active {
			return None()
		}
		if d.Safe == 0 {
			return None()
		}
		b.revealed++
		b.waiting = false
		if b.revealed >= b.maxReveals {
			return BetAction(b.betID, []byte{2}) // cashout
		}
		tile := revealOrder[b.revealed%len(revealOrder)]
		return BetAction(b.betID, []byte{1, tile})

	case "settled":
		var d struct {
			BetID uint64 `json:"bet_id"`
		}
		json.Unmarshal(data, &d)
		if d.BetID == b.betID {
			b.active = false
			b.betID = 0
		}
		return None()

	case "block":
		if b.active && b.pendingReveal {
			b.pendingReveal = false
			b.waiting = true
			tile := revealOrder[0]
			return BetAction(b.betID, []byte{1, tile})
		}
		if !b.active && !b.waiting {
			b.counter++
			if b.counter >= b.every {
				b.counter = 0
				stake := b.minStake
				if b.maxStake > b.minStake {
					stake = b.minStake + uint64(b.rng.Int63n(int64(b.maxStake-b.minStake)))
				}
				return PlaceBet(stake, []byte{byte(b.minesCount)})
			}
		}
		return None()

	default:
		return None()
	}
}
