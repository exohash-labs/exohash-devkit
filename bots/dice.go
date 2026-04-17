package bots

import (
	"encoding/binary"
	"encoding/json"
	"math/rand"
)

// DiceBot places random dice bets every N blocks.
type DiceBot struct {
	addr       string
	calcID     uint64
	bankrollID uint64
	minStake   uint64
	maxStake uint64
	chance   uint64 // chance in bp (e.g. 5000 = 50%)
	every    int
	counter  int
	rng      *rand.Rand
}

type DiceBotConfig struct {
	Address    string
	CalcID     uint64
	BankrollID uint64
	MinStake   uint64
	MaxStake uint64
	ChanceBP uint64
	Every    int
	Seed     int64
}

func NewDiceBot(cfg DiceBotConfig) *DiceBot {
	every := cfg.Every
	if every <= 0 {
		every = 10
	}
	chance := cfg.ChanceBP
	if chance == 0 {
		chance = 5000
	}
	return &DiceBot{
		addr:       cfg.Address,
		calcID:     cfg.CalcID,
		bankrollID: cfg.BankrollID,
		minStake: cfg.MinStake,
		maxStake: cfg.MaxStake,
		chance:   chance,
		every:    every,
		rng:      rand.New(rand.NewSource(cfg.Seed)),
	}
}

func (b *DiceBot) Address() string    { return b.addr }
func (b *DiceBot) CalcID() uint64     { return b.calcID }
func (b *DiceBot) BankrollID() uint64 { return b.bankrollID }
func (b *DiceBot) SetBetID(uint64)    {}

func (b *DiceBot) OnEvent(topic string, data json.RawMessage) Action {
	if topic != "block" {
		return None()
	}
	b.counter++
	if b.counter < b.every {
		return None()
	}
	b.counter = 0

	stake := b.minStake
	if b.maxStake > b.minStake {
		stake = b.minStake + uint64(b.rng.Int63n(int64(b.maxStake-b.minStake)))
	}

	params := make([]byte, 9)
	params[0] = 2 // mode = over
	binary.LittleEndian.PutUint64(params[1:], b.chance)

	return PlaceBet(stake, params)
}
