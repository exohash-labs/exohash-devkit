package chainsim

import "fmt"

// Deposit credits USDC to an account. Creates the account if it doesn't exist.
// On real chain: IBC transfer or faucet.
func (c *Chain) Deposit(addr string, amount uint64) error {
	if addr == "" {
		return fmt.Errorf("address required")
	}
	if amount == 0 {
		return fmt.Errorf("amount must be > 0")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	acc, ok := c.accounts[addr]
	if !ok {
		acc = &Account{Address: addr}
		c.accounts[addr] = acc
	}
	acc.Balance += amount
	return nil
}

// Withdraw debits USDC from an account.
func (c *Chain) Withdraw(addr string, amount uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	acc, ok := c.accounts[addr]
	if !ok {
		return fmt.Errorf("unknown address %s", addr)
	}
	if acc.Balance < amount {
		return fmt.Errorf("insufficient balance: %d < %d", acc.Balance, amount)
	}
	acc.Balance -= amount
	return nil
}

// Balance returns the USDC balance for an address.
func (c *Chain) Balance(addr string) (uint64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	acc, ok := c.accounts[addr]
	if !ok {
		return 0, fmt.Errorf("unknown address %s", addr)
	}
	return acc.Balance, nil
}

// GetAccount returns account info. Returns nil if not found.
func (c *Chain) GetAccount(addr string) *Account {
	c.mu.RLock()
	defer c.mu.RUnlock()

	acc := c.accounts[addr]
	if acc == nil {
		return nil
	}
	cp := *acc
	return &cp
}
