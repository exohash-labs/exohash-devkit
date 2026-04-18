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

	// AutoRefundBlocks: if the beacon is unavailable for longer than this many
	// blocks, all open bets are refunded and block_update is skipped.
	// Mirrors x/house/types.DefaultAutoRefundBlocks (172800 = 24h at 500ms).
	AutoRefundBlocks int64

	// GasInitialCredits: gas granted to a calculator at deploy time.
	// Mirrors x/house/types.DefaultGasInitialCredits (1B).
	GasInitialCredits uint64

	// GasCreditPerBet: gas credit added on each successful place_bet.
	// Mirrors x/house/types.DefaultGasCreditPerBet (1M).
	GasCreditPerBet uint64

	// PerCalcWasmGasPerBlock: aggregate cap on WASM gas consumed by one
	// calculator across all WASM calls (block_update + every place_bet +
	// every bet_action) within a single block. Reset at AdvanceBlock entry.
	// Exceeding this kills the calculator (matches existing per-call kill
	// behavior, just scoped to whole-block aggregate now). Default 10M.
	PerCalcWasmGasPerBlock uint64

	// PerCalcSdkGasPerBlock: aggregate cap on SDK store gas consumed by
	// one calculator across all WASM calls within a single block. SDK gas
	// is charged on every host KV op (kv_get / kv_set / kv_delete / kv_has)
	// using the Cosmos IAVL schedule (read=1000+bytes×30, write=2000+bytes×30,
	// delete=1000, has=500). Exceeding this kills the calculator. Default 10M.
	PerCalcSdkGasPerBlock uint64
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
		MaxKVBytesPerCalculator: 1_048_576,      // 1 MB
		AutoRefundBlocks:        172800,          // 24h at 500ms blocks
		GasInitialCredits:       1_000_000_000,   // 1B gas on deploy
		GasCreditPerBet:         1_000_000,       // 1M gas per successful place_bet
		PerCalcWasmGasPerBlock:  10_000_000,      // 10M aggregate WASM gas per calc per block
		PerCalcSdkGasPerBlock:   10_000_000,      // 10M aggregate SDK gas per calc per block
	}
}
