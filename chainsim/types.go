package chainsim

// Bankroll mirrors x/house/types.Bankroll.
type Bankroll struct {
	ID                     uint64
	Creator                string
	Balance                uint64 // total USDC in escrow
	TotalReserved          uint64 // locked for pending bets
	MaxPayoutCapBps        uint32 // max single bet payout as % of balance (default 200 = 2%)
	MaxReservedBps         uint32 // max total reserved as % of balance (default 8000 = 80%)
	TotalShares            uint64 // LP shares outstanding
	IsPrivate              bool
	PendingWithdrawalTotal uint64
	Name                   string
	Games                  map[uint64]bool // attached calcIDs
}

// Available returns the bankroll's available liquidity for new bets.
func (b *Bankroll) Available() uint64 {
	if b.Balance <= b.TotalReserved+b.PendingWithdrawalTotal {
		return 0
	}
	return b.Balance - b.TotalReserved - b.PendingWithdrawalTotal
}

// Bet mirrors x/house/types.Bet.
type Bet struct {
	ID           uint64
	BankrollID   uint64
	CalculatorID uint64
	Bettor       string
	Stake        uint64
	Reserved     uint64 // max payout reserved from bankroll
	ValFee       uint64
	ProtoFee     uint64
	NetStake     uint64 // stake - fees
	Payout       uint64
	Status       BetStatus
	EntryState   []byte // raw params
}

// BetStatus represents the current state of a bet.
type BetStatus int

const (
	BetOpen     BetStatus = 0
	BetSettled  BetStatus = 1
	BetRefunded BetStatus = 2
)

// Settlement kind values — match WASM settle() kind parameter.
const (
	SettleKindWin    uint8 = 1
	SettleKindLoss   uint8 = 2
	SettleKindRefund uint8 = 3
)

func (s BetStatus) String() string {
	switch s {
	case BetOpen:
		return "open"
	case BetSettled:
		return "settled"
	case BetRefunded:
		return "refunded"
	default:
		return "unknown"
	}
}

// Account tracks a player's USDC balance.
type Account struct {
	Address string
	Balance uint64
}

// FeeSplit mirrors x/house/keeper.FeeSplit.
type FeeSplit struct {
	ValFee      uint64
	ProtoFee    uint64
	BankrollNet uint64
}

// CalculatorStatus represents the lifecycle state of a calculator.
type CalculatorStatus int

const (
	CalcStatusActive CalculatorStatus = 0
	CalcStatusPaused CalculatorStatus = 1
	CalcStatusKilled CalculatorStatus = 2
)

// Calculator mirrors x/house/types.CalculatorInfo.
type Calculator struct {
	ID          uint64
	Name        string
	Engine      string
	HouseEdgeBp uint64
	Status      CalculatorStatus
}

// UserShares tracks LP shares per address per bankroll.
type UserShares struct {
	BankrollID uint64
	Address    string
	Shares     uint64
}
