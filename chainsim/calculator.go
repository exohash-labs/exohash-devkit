package chainsim

import "fmt"

// RegisterCalculator registers a game calculator.
// Mirrors MsgRegisterCalculator.
func (c *Chain) RegisterCalculator(calc Calculator) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if calc.ID == 0 {
		return fmt.Errorf("calculator ID must be > 0")
	}
	if _, exists := c.calculators[calc.ID]; exists {
		return fmt.Errorf("calculator %d already exists", calc.ID)
	}

	// Name uniqueness.
	for _, existing := range c.calculators {
		if existing.Name == calc.Name {
			return fmt.Errorf("calculator name %q already taken by calculator %d", calc.Name, existing.ID)
		}
	}

	calc.Status = CalcStatusActive
	c.calculators[calc.ID] = &calc
	return nil
}

// GetCalculator returns a calculator by ID.
func (c *Chain) GetCalculator(calcID uint64) (*Calculator, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	calc, ok := c.calculators[calcID]
	if !ok {
		return nil, fmt.Errorf("calculator %d not found", calcID)
	}
	return calc, nil
}

// PauseCalculator stops new bets but lets existing bets settle normally.
func (c *Chain) PauseCalculator(calcID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	if calc.Status == CalcStatusKilled {
		return fmt.Errorf("calculator %d is killed, cannot pause", calcID)
	}
	calc.Status = CalcStatusPaused
	c.emit("calculator_paused", "calculator_id", u64(calcID))
	return nil
}

// ResumeCalculator re-enables new bets on a paused calculator.
func (c *Chain) ResumeCalculator(calcID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	if calc.Status != CalcStatusPaused {
		return fmt.Errorf("calculator %d is not paused", calcID)
	}
	calc.Status = CalcStatusActive
	c.emit("calculator_resumed", "calculator_id", u64(calcID))
	return nil
}

// KillCalculator permanently kills a calculator and refunds all open bets.
func (c *Chain) KillCalculator(calcID uint64, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.killCalculatorLocked(calcID, reason)
}

func (c *Chain) killCalculatorLocked(calcID uint64, reason string) error {
	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	calc.Status = CalcStatusKilled
	c.emit("calculator_killed",
		"calculator_id", u64(calcID),
		"reason", reason,
	)

	// Refund all open bets for this calculator.
	c.refundOpenBetsLocked(calcID)
	return nil
}

// refundOpenBetsLocked settles all open bets as refunds. Caller must hold c.mu.
func (c *Chain) refundOpenBetsLocked(calcID uint64) {
	savedMode := c.mode
	savedCalcID := c.activeCalcID
	c.mode = CalcModeBlockUpdate
	c.activeCalcID = calcID // ownership check in settleLocked needs this

	for betID, bet := range c.bets {
		if bet.CalculatorID != calcID || bet.Status != BetOpen {
			continue
		}
		_ = c.settleLocked(betID, 0, SettleKindRefund)
	}

	c.mode = savedMode
	c.activeCalcID = savedCalcID
}
