package chainsim

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// Block tracks the current block state.
type Block struct {
	Height     uint64
	BeaconSeed [32]byte // deterministic RNG for this block
}

// AdvanceBlock increments height, derives new beacon seed, executes
// WASM block_update for all games with pending wakeups, and returns
// all events atomically (no race with concurrent PlaceBet/BetAction).
func (c *Chain) AdvanceBlock() BlockResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.height++
	// Reset per-block aggregate accounting at the boundary. block_update
	// (run below) and any subsequent place_bet/bet_action calls before the
	// next AdvanceBlock all share these counters per calculator.
	c.blockWasmUsed = make(map[uint64]uint64)
	c.blockSdkUsed = make(map[uint64]uint64)
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:], c.seed)
	binary.LittleEndian.PutUint64(buf[8:], c.height)
	seed := sha256.Sum256(buf[:])

	block := Block{
		Height:     c.height,
		BeaconSeed: seed,
	}

	// Beacon circuit breaker — mirrors keeper/block_update.go ProcessActiveCalculators.
	// If the beacon has been down longer than AutoRefundBlocks, refund all open bets.
	// Otherwise, if it's merely down, skip block_update and emit circuit_breaker.
	if c.beaconDown {
		downBlocks := int64(c.height - c.beaconDownSince)
		if c.params.AutoRefundBlocks > 0 && downBlocks > c.params.AutoRefundBlocks {
			c.autoRefundAllOpen()
			c.emit("beacon_auto_refund", "height", u64(c.height))
		} else {
			c.emit("beacon_circuit_breaker",
				"status", "paused",
				"height", u64(c.height),
			)
		}
		return c.drainBlockResult(block)
	}

	// Call block_update(seed) for every registered game.
	c.mode = CalcModeBlockUpdate
	for calcID, game := range c.games {
		// Skip killed calculators.
		if calc, ok := c.calculators[calcID]; ok && calc.Status != CalcStatusActive {
			continue
		}

		c.activeCalcID = calcID
		ctx, _, _ := c.wasmCtxForGame(calcID)
		budget := c.computeGasBudget(calcID)
		// If no WASM budget remains for this calc this block, skip block_update.
		// (Shouldn't happen at AdvanceBlock entry since counters were just
		// reset, unless gasBalance is zero — in which case the calc gets killed
		// by the existing balance-exhausted path on the next call.)
		if budget == 0 {
			continue
		}
		c.currentGasBudget = budget

		var gasBefore uint64
		if game.inst.gasGlobal != nil {
			gasBefore = game.inst.gasGlobal.Get()
		}
		c.lastWasmGas = gasBefore

		if err := game.inst.callBlockUpdate(ctx, seed[:]); err != nil {
			fmt.Printf("block_update error (calc=%d, h=%d): %v\n", calcID, c.height, err)
		}

		used := c.totalGasUsed(calcID)
		c.currentGasBudget = 0 // clear so WASM can't read stale budget
		c.blockWasmUsed[calcID] += used
		if !c.deductGas(calcID, used) {
			_ = c.killCalculatorLocked(calcID, "gas_balance_exhausted")
		} else if c.blockWasmUsed[calcID] > c.params.PerCalcWasmGasPerBlock {
			_ = c.killCalculatorLocked(calcID, "wasm_gas_per_block_exceeded")
		} else if c.blockSdkUsed[calcID] > c.params.PerCalcSdkGasPerBlock {
			_ = c.killCalculatorLocked(calcID, "sdk_gas_per_block_exceeded")
		}

		c.reinstantiateIfNeeded(calcID)
	}

	return c.drainBlockResult(block)
}

// drainBlockResult snapshots and clears event buffers.
// Caller must hold c.mu.
func (c *Chain) drainBlockResult(block Block) BlockResult {
	calcEvents := make([]CalcEvent, len(c.calcEvents))
	copy(calcEvents, c.calcEvents)
	c.calcEvents = c.calcEvents[:0]

	settlements := make([]Settlement, len(c.settlements))
	copy(settlements, c.settlements)
	c.settlements = c.settlements[:0]

	chainEvents := make([]ChainEvent, len(c.events))
	copy(chainEvents, c.events)
	c.events = c.events[:0]

	return BlockResult{
		Block:       block,
		CalcEvents:  calcEvents,
		Settlements: settlements,
		Events:      chainEvents,
	}
}

// autoRefundAllOpen refunds every open bet. Mirrors keeper.RefundOpenBets
// invoked from block_update.go when the beacon has been down too long.
// Caller must hold c.mu.
func (c *Chain) autoRefundAllOpen() {
	c.mode = CalcModeBlockUpdate
	for betID, bet := range c.bets {
		if bet.Status != BetOpen {
			continue
		}
		c.activeCalcID = bet.CalculatorID
		_ = c.settleLocked(betID, 0, SettleKindRefund)
	}
}

// Height returns current block height.
func (c *Chain) Height() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.height
}

// BeaconSeedHex returns the beacon seed for a given height as hex string.
func (c *Chain) BeaconSeedHex(height uint64) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:], c.seed)
	binary.LittleEndian.PutUint64(buf[8:], height)
	seed := sha256.Sum256(buf[:])
	return hex.EncodeToString(seed[:])
}

// getRNGLocked returns 32 bytes of deterministic randomness for a height.
// Mode gate: only allowed during CalcModeBlockUpdate.
// Caller must hold c.mu.
func (c *Chain) getRNGLocked(height uint64) []byte {
	if c.mode != CalcModeBlockUpdate {
		return nil
	}
	if height >= c.height {
		return nil
	}

	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:], c.seed)
	binary.LittleEndian.PutUint64(buf[8:], height)
	seed := sha256.Sum256(buf[:])
	return seed[:]
}
