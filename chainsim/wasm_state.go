package chainsim

// ---------------------------------------------------------------------------
// Wakeup scheduling — mirrors keeper BetWakeupsByHeight
// ---------------------------------------------------------------------------

// ScheduleWakeup registers a bet for processing at a future block height.
// If height is 0, schedules for the next block.
func (c *Chain) ScheduleWakeup(betID, height uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.scheduleWakeupLocked(betID, height)
}

func (c *Chain) scheduleWakeupLocked(betID, height uint64) {
	if height == 0 || height <= c.height {
		// Wakeup at 0 or at/before current height → schedule for next block.
		// This handles WASM re-scheduling within block_update.
		height = c.height + 1
	}
	c.wakeups[height] = append(c.wakeups[height], betID)
}

// CancelWakeup removes a bet from the wakeup schedule.
func (c *Chain) CancelWakeup(betID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelWakeupLocked(betID)
}

func (c *Chain) cancelWakeupLocked(betID uint64) {
	for height, ids := range c.wakeups {
		for i, id := range ids {
			if id == betID {
				c.wakeups[height] = append(ids[:i], ids[i+1:]...)
				return
			}
		}
	}
}

// CollectWakeups returns and removes all bet IDs due at the given height.
func (c *Chain) CollectWakeups(height uint64) []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	ids := c.wakeups[height]
	delete(c.wakeups, height)
	return ids
}

// CollectWakeupsByCalc returns and removes all bet IDs due at the given height,
// grouped by calculator ID. Mirrors keeper ProcessV3BetWakeups grouping.
func (c *Chain) CollectWakeupsByCalc(height uint64) map[uint64][]uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.collectWakeupsByCalcLocked(height)
}

func (c *Chain) collectWakeupsByCalcLocked(height uint64) map[uint64][]uint64 {
	ids := c.wakeups[height]
	delete(c.wakeups, height)

	if len(ids) == 0 {
		return nil
	}

	grouped := make(map[uint64][]uint64)
	for _, betID := range ids {
		calcID := c.betGame[betID]
		grouped[calcID] = append(grouped[calcID], betID)
	}
	return grouped
}

// SetWakeupBetIDs sets the current block_update batch for GetBetCount/GetBetID.
func (c *Chain) SetWakeupBetIDs(ids []uint64) {
	c.mu.Lock()
	c.wakeupBetIDs = ids
	c.mu.Unlock()
}

// getBetCountLocked returns the number of bets in the current block_update batch.
// Caller must hold c.mu.
func (c *Chain) getBetCountLocked() uint32 {
	if c.mode != CalcModeBlockUpdate {
		return 0
	}
	return uint32(len(c.wakeupBetIDs))
}

// getBetIDLocked returns the bet ID at the given index in the wakeup batch.
// Caller must hold c.mu.
func (c *Chain) getBetIDLocked(index uint32) uint64 {
	if c.mode != CalcModeBlockUpdate {
		return 0
	}
	if int(index) >= len(c.wakeupBetIDs) {
		return 0
	}
	return c.wakeupBetIDs[index]
}

// ---------------------------------------------------------------------------
// Pending actions — mirrors keeper EngineKV pending_action/{betID}
// ---------------------------------------------------------------------------

// SetPendingAction queues an action for a bet, consumed by next block_update.
func (c *Chain) SetPendingAction(betID uint64, action []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.pendingActions[betID] = action
}

// getPendingActionLocked returns and removes a queued action for a bet.
// Caller must hold c.mu.
func (c *Chain) getPendingActionLocked(betID uint64) []byte {
	if c.mode != CalcModeBlockUpdate {
		return nil
	}
	bet, ok := c.bets[betID]
	if !ok || bet.CalculatorID != c.activeCalcID {
		return nil
	}
	action, ok := c.pendingActions[betID]
	if ok {
		delete(c.pendingActions, betID)
	}
	return action
}

// ---------------------------------------------------------------------------
// Bettor lookup
// ---------------------------------------------------------------------------

// GetBettor returns the bettor address for a bet ID.
func (c *Chain) GetBettor(betID uint64) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getBettorLocked(betID)
}

func (c *Chain) getBettorLocked(betID uint64) string {
	bet, ok := c.bets[betID]
	if !ok {
		return ""
	}
	if bet.CalculatorID != c.activeCalcID {
		return ""
	}
	return bet.Bettor
}

// ---------------------------------------------------------------------------
// Calc events — mirrors SDK EventTypeCalcEvent from host_emit_event
// ---------------------------------------------------------------------------

// EmitCalcEvent records a WASM calculator event.
func (c *Chain) EmitCalcEvent(topic, data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emitCalcEventLocked(topic, data)
}

func (c *Chain) emitCalcEventLocked(topic, data string) {
	c.calcEvents = append(c.calcEvents, CalcEvent{CalcID: c.activeCalcID, Topic: topic, Data: data})
}

// DrainCalcEvents returns collected calc events and clears the buffer.
func (c *Chain) DrainCalcEvents() []CalcEvent {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]CalcEvent, len(c.calcEvents))
	copy(out, c.calcEvents)
	c.calcEvents = c.calcEvents[:0]
	return out
}

// ---------------------------------------------------------------------------
// Settlements — drain buffer for caller tracking
// ---------------------------------------------------------------------------

// RecordSettlement appends a settlement record. Called after Settle() succeeds.
func (c *Chain) RecordSettlement(betID, payout uint64, kind uint8) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.settlements = append(c.settlements, Settlement{BetID: betID, Payout: payout, Kind: kind})
}

// DrainSettlements returns settlements since last drain and clears.
func (c *Chain) DrainSettlements() []Settlement {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]Settlement, len(c.settlements))
	copy(out, c.settlements)
	c.settlements = c.settlements[:0]
	return out
}
