// Package chainsim provides a lightweight in-memory simulation of the ExoHash
// chain state — bankrolls, accounts, solvency, fees, settlements, and WASM
// game execution — without Cosmos SDK dependencies.
package chainsim

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
)

// ErrBeaconUnavailable mirrors x/house/types.ErrBeaconUnavailable —
// returned when bets are rejected because the DKG beacon is down.
var ErrBeaconUnavailable = errors.New("beacon randomness unavailable — games paused")

// CalcMode determines which operations are available to the WASM calculator.
// Mirrors x/house/keeper.CalcMode. The host enforces these gates so that
// WASM developers cannot access RNG outside of block_update.
type CalcMode uint8

const (
	CalcModePlaceBet    CalcMode = 1 // MsgPlaceBet — player joining a game
	CalcModeBetAction   CalcMode = 2 // MsgBetAction — player in-game action
	CalcModeBlockUpdate CalcMode = 3 // BeginBlock — seed-based tick processing
	CalcModeInitGame    CalcMode = 4 // Kickstart — one-time game setup
)

// CalcEvent is a WASM calculator event (topic + JSON data).
// Mirrors SDK EventTypeCalcEvent emitted via host_emit_event.
type CalcEvent struct {
	CalcID uint64 // which game emitted this event
	Topic  string
	Data   string
}

// Settlement records a bet settlement for caller tracking.
type Settlement struct {
	BetID  uint64
	CalcID uint64
	Payout uint64
	Kind   uint8 // 1=win, 2=loss, 3=refund
}

// BlockResult contains everything produced by a single AdvanceBlock call.
type BlockResult struct {
	Block       Block
	CalcEvents  []CalcEvent
	Settlements []Settlement
	Events      []ChainEvent
}

// Chain is the in-memory chain state with integrated WASM execution.
// All operations are thread-safe.
type Chain struct {
	params      Params
	bankrolls   map[uint64]*Bankroll
	accounts    map[string]*Account
	bets        map[uint64]*Bet
	calculators map[uint64]*Calculator
	userShares  map[string]uint64 // "bankrollID:address" → shares

	betsByAddr map[string][]uint64 // address → []betID
	betGame    map[uint64]uint64   // betID → calcID

	nextBankrollID uint64
	nextBetID      uint64

	// Block state.
	height uint64
	seed   uint64 // RNG seed (deterministic)

	// Beacon availability. Mirrors the chain's DKG circuit breaker:
	// when false, PlaceBet/BetAction return ErrBeaconUnavailable and
	// block_update is skipped until beacon is restored. If the beacon
	// stays down for > params.AutoRefundBlocks, all open bets are
	// refunded on the next AdvanceBlock.
	beaconDown      bool
	beaconDownSince uint64 // height at which beacon went down

	// Execution mode — mirrors keeper CalcMode.
	mode         CalcMode
	activeCalcID uint64 // which game is currently executing WASM

	// Pending actions — mirrors keeper EngineKV pending_action/{betID}.
	pendingActions map[uint64][]byte

	// Fee accumulators (mirrors valrewards/exohrewards modules).
	TotalValFees   uint64
	TotalProtoFees uint64

	// Event logs — drained by caller.
	events      []ChainEvent
	calcEvents  []CalcEvent
	settlements []Settlement

	// KV storage usage per calculator (bytes).
	kvUsage map[uint64]uint64

	// Gas metering — single counter in the WASM gas_used global.
	// chargeGas writes host costs directly into the WASM global.
	lastWasmGas      uint64            // gas_used global snapshot before last call
	gasBalance       map[uint64]uint64 // calcID → remaining gas credits
	currentGasBudget uint64            // budget for current WASM call (read by host fn)

	// Per-block aggregate accounting (reset at start of every AdvanceBlock).
	// blockWasmUsed tracks WASM gas consumed by each calc across all WASM
	// calls in this block (block_update + place_bets + bet_actions).
	// blockSdkUsed tracks SDK store gas (kv_get/set/delete/has) per calc.
	// Either exceeding its cap kills the calc.
	blockWasmUsed map[uint64]uint64
	blockSdkUsed  map[uint64]uint64

	// WASM runtime + registered games.
	wasmRT wazero.Runtime
	games  map[uint64]*gameState

	mu sync.RWMutex
}

// SetMode sets the current execution mode. Must be called before
// WASM host callback operations (Reserve, Settle, GetRNG, etc.).
func (c *Chain) SetMode(mode CalcMode) {
	c.mu.Lock()
	c.mode = mode
	c.mu.Unlock()
}

// SetBeaconAvailable toggles the beacon circuit breaker.
// When false, PlaceBet/BetAction return ErrBeaconUnavailable and
// block_update is skipped. Mirrors the chain's BeaconLiveForBets gate.
// Default is true (beacon up).
func (c *Chain) SetBeaconAvailable(available bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !available && !c.beaconDown {
		c.beaconDown = true
		c.beaconDownSince = c.height
	} else if available {
		c.beaconDown = false
		c.beaconDownSince = 0
	}
}

// New creates a new chain simulator with default params and seed 42.
func New() *Chain {
	return NewWithParams(DefaultParams(), 42)
}

// NewWithParams creates a new chain simulator with custom params.
func NewWithParams(params Params, seed uint64) *Chain {
	ctx := context.Background()
	rt, err := newWasmRuntime(ctx)
	if err != nil {
		panic(fmt.Sprintf("failed to create WASM runtime: %v", err))
	}

	return &Chain{
		params:         params,
		bankrolls:      make(map[uint64]*Bankroll),
		accounts:       make(map[string]*Account),
		bets:           make(map[uint64]*Bet),
		calculators:    make(map[uint64]*Calculator),
		userShares:     make(map[string]uint64),
		betsByAddr:     make(map[string][]uint64),
		betGame:        make(map[uint64]uint64),
		pendingActions: make(map[uint64][]byte),
		kvUsage:        make(map[uint64]uint64),
		gasBalance:     make(map[uint64]uint64),
		blockWasmUsed:  make(map[uint64]uint64),
		blockSdkUsed:   make(map[uint64]uint64),
		nextBankrollID: 1,
		nextBetID:      1,
		seed:           seed,
		wasmRT:         rt,
		games:          make(map[uint64]*gameState),
	}
}

// Close releases WASM runtime resources.
func (c *Chain) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	ctx := context.Background()
	for _, g := range c.games {
		if g.inst != nil {
			g.inst.close(ctx)
		}
	}
	if c.wasmRT != nil {
		c.wasmRT.Close(ctx)
		c.wasmRT = nil
	}
}

// Params returns current params.
func (c *Chain) Params() Params {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.params
}

// KVUsage returns the total KV bytes used by a calculator.
func (c *Chain) KVUsage(calcID uint64) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.kvUsage[calcID]
}

// GasBalance returns the remaining gas credits for a calculator.
func (c *Chain) GasBalance(calcID uint64) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gasBalance[calcID]
}

// WasmGasUsed returns total accumulated gas for a game (WASM + host unified).
func (c *Chain) WasmGasUsed(calcID uint64) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	game, ok := c.games[calcID]
	if !ok || game.inst == nil || game.inst.gasGlobal == nil {
		return 0
	}
	return game.inst.gasGlobal.Get()
}

// WasmMemorySize returns the current WASM linear memory size in bytes for a game.
func (c *Chain) WasmMemorySize(calcID uint64) uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	game, ok := c.games[calcID]
	if !ok || game.inst == nil || game.inst.mod == nil {
		return 0
	}
	return game.inst.mod.Memory().Size()
}

// BlockWasmUsed returns WASM gas consumed by this calculator in the current
// block (sum across block_update + every place_bet/bet_action since the last
// AdvanceBlock). Reset on each AdvanceBlock.
func (c *Chain) BlockWasmUsed(calcID uint64) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blockWasmUsed[calcID]
}

// BlockSdkUsed returns SDK store gas consumed by this calculator in the
// current block. Reset on each AdvanceBlock.
func (c *Chain) BlockSdkUsed(calcID uint64) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.blockSdkUsed[calcID]
}

// computeGasBudget returns the WASM gas budget for a single WASM call to
// this calculator. Capped by:
//  1. remaining per-block aggregate room (PerCalcWasmGasPerBlock - already used this block)
//  2. remaining long-term gas balance
//
// Returns 0 if the calc is out of either budget — caller must reject the call.
func (c *Chain) computeGasBudget(calcID uint64) uint64 {
	bal := c.gasBalance[calcID]
	used := c.blockWasmUsed[calcID]
	cap := c.params.PerCalcWasmGasPerBlock
	var blockRemaining uint64
	if used < cap {
		blockRemaining = cap - used
	}
	budget := blockRemaining
	if bal < budget {
		budget = bal
	}
	return budget
}

// chargeSdkGas adds amount to the per-calc per-block SDK gas counter.
// Called by host KV functions to mirror Cosmos IAVL gas charging.
// Caller must hold c.mu.
func (c *Chain) chargeSdkGas(calcID, amount uint64) {
	c.blockSdkUsed[calcID] += amount
}

// deductGas subtracts used gas from the calculator's balance.
// Returns false if balance insufficient (calculator should be killed).
func (c *Chain) deductGas(calcID, used uint64) bool {
	bal := c.gasBalance[calcID]
	if used > bal {
		c.gasBalance[calcID] = 0
		return false
	}
	c.gasBalance[calcID] = bal - used
	return true
}

// creditGas adds gas credits to a calculator's balance (saturating).
func (c *Chain) creditGas(calcID, amount uint64) {
	bal := c.gasBalance[calcID]
	if bal > ^uint64(0)-amount {
		c.gasBalance[calcID] = ^uint64(0) // saturate
	} else {
		c.gasBalance[calcID] = bal + amount
	}
}

// totalGasUsed returns the gas delta for the current call (WASM + host unified).
// Returns MaxUint64 on underflow (game manipulated gas_used global downward) → forces kill.
// Caller must hold c.mu.
func (c *Chain) totalGasUsed(calcID uint64) uint64 {
	game, ok := c.games[calcID]
	if !ok || game.inst == nil || game.inst.gasGlobal == nil {
		return 0
	}
	current := game.inst.gasGlobal.Get()
	if current < c.lastWasmGas {
		return ^uint64(0) // underflow → force kill
	}
	return current - c.lastWasmGas
}

func sharesKey(bankrollID uint64, addr string) string {
	return fmt.Sprintf("%d:%s", bankrollID, addr)
}
