package chainsim

// Params mirrors x/house/types.Params — protocol-level configuration.
// All values in basis points (1 bp = 0.01%).
type Params struct {
	// ProtocolFeeBp: flat fee on wagering volume (default 25 = 0.25%).
	ProtocolFeeBp uint32

	// ValFeeBp: portion of protocol fee to validators (default 5 = 0.05%).
	ValFeeBp uint32

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
		ProtocolFeeBp:           25,
		ValFeeBp:                5,
		MaxPayoutCapBpsMax:      200,
		MaxReservedBpsMax:       8000,
		MinDepositAmount:        10_000_000,  // 10 USDC
		BankrollCreationFee:     0,
		MinStakeUusdc:           100_000,     // 0.10 USDC
		MaxKVBytesPerCalculator: 1_048_576,   // 1 MB
	}
}
