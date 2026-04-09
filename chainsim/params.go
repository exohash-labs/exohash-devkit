package chainsim

// Params mirrors x/house/types.Params — protocol-level configuration.
// All values in basis points (1 bp = 0.01%).
type Params struct {
	// TakeRateOfEdgeBp: what % of the house edge is collected as protocol fee.
	// Default: 2500 (25% of edge).
	TakeRateOfEdgeBp uint32

	// FeeSplitValrewardsBp: how to split the protocol fee between validators and EXOH stakers.
	// Default: 5000 (50/50).
	FeeSplitValrewardsBp uint32

	// MaxPayoutCapBpsMax: global max for per-bankroll MaxPayoutCapBps.
	MaxPayoutCapBpsMax uint32

	// MaxReservedBpsMax: global max for per-bankroll MaxReservedBps.
	MaxReservedBpsMax uint32

	// MinDepositAmount: minimum per-deposit amount (anti-spam).
	MinDepositAmount uint64

	// BankrollCreationFee: burned on bankroll creation (anti-spam).
	BankrollCreationFee uint64

	// MinStakeUusdc: minimum bet stake in uusdc (default 100_000 = 0.10 USDC).
	MinStakeUusdc uint64

	// MaxKVBytesPerCalculator: KV storage budget per calculator.
	// Exceeding this kills the calculator and refunds all open bets.
	// Default: 1_048_576 (1 MB).
	MaxKVBytesPerCalculator uint64
}

// DefaultParams returns the chain defaults.
func DefaultParams() Params {
	return Params{
		TakeRateOfEdgeBp:        2500,
		FeeSplitValrewardsBp:    5000,
		MaxPayoutCapBpsMax:      200,
		MaxReservedBpsMax:       8000,
		MinDepositAmount:        10_000_000,  // 10 USDC
		BankrollCreationFee:     0,
		MinStakeUusdc:           100_000,     // 0.10 USDC
		MaxKVBytesPerCalculator: 1_048_576,   // 1 MB
	}
}
