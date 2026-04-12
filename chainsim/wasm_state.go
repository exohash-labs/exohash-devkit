package chainsim

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
