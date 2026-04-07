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
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:], c.seed)
	binary.LittleEndian.PutUint64(buf[8:], c.height)
	seed := sha256.Sum256(buf[:])

	block := Block{
		Height:     c.height,
		BeaconSeed: seed,
	}

	// Process WASM wakeups — mirrors keeper ProcessV3BetWakeups.
	c.mode = CalcModeBlockUpdate
	byCalc := c.collectWakeupsByCalcLocked(c.height)
	for calcID, ids := range byCalc {
		game, ok := c.games[calcID]
		if !ok {
			continue
		}
		c.activeCalcID = calcID
		c.wakeupBetIDs = ids
		ctx, _, _ := c.wasmCtxForGame(calcID)
		if err := game.inst.callBlockUpdate(ctx, c.height); err != nil {
			fmt.Printf("block_update error (calc=%d, h=%d): %v\n", calcID, c.height, err)
		}
	}
	c.wakeupBetIDs = nil

	// Drain events while still holding the lock — prevents concurrent
	// PlaceBet/BetAction from stealing block_update events.
	events := make([]CalcEvent, len(c.calcEvents))
	copy(events, c.calcEvents)
	c.calcEvents = c.calcEvents[:0]

	settlements := make([]Settlement, len(c.settlements))
	copy(settlements, c.settlements)
	c.settlements = c.settlements[:0]

	return BlockResult{
		Block:       block,
		CalcEvents:  events,
		Settlements: settlements,
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
