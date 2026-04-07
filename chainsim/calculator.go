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

	calc.Active = true
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

// KillCalculator marks a calculator as inactive (fraud detection).
// Mirrors keeper.KillCalculator.
func (c *Chain) KillCalculator(calcID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	calc.Active = false
	return nil
}
