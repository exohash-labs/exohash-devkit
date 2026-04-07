package chainsim

import "fmt"

const (
	DefaultMaxPayoutCapBps uint32 = 200  // 2% of bankroll per bet
	DefaultMaxReservedBps  uint32 = 8000 // 80% of bankroll total reserved
)

// CreateBankroll creates a new bankroll with an initial deposit.
// Mirrors MsgCreateBankroll.
func (c *Chain) CreateBankroll(owner string, initialDeposit uint64, name string, isPrivate bool) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Charge creation fee from owner's account.
	if c.params.BankrollCreationFee > 0 {
		acc, ok := c.accounts[owner]
		if !ok {
			return 0, fmt.Errorf("unknown address %s", owner)
		}
		if acc.Balance < c.params.BankrollCreationFee {
			return 0, fmt.Errorf("insufficient balance for creation fee")
		}
		acc.Balance -= c.params.BankrollCreationFee
		// Fee is burned (not credited anywhere).
	}

	// Debit initial deposit from owner.
	if initialDeposit > 0 {
		acc, ok := c.accounts[owner]
		if !ok {
			return 0, fmt.Errorf("unknown address %s", owner)
		}
		if acc.Balance < initialDeposit {
			return 0, fmt.Errorf("insufficient balance for initial deposit: %d < %d", acc.Balance, initialDeposit)
		}
		acc.Balance -= initialDeposit
	}

	id := c.nextBankrollID
	c.nextBankrollID++

	br := &Bankroll{
		ID:              id,
		Creator:         owner,
		Balance:         initialDeposit,
		TotalReserved:   0,
		MaxPayoutCapBps: DefaultMaxPayoutCapBps,
		MaxReservedBps:  DefaultMaxReservedBps,
		TotalShares:     initialDeposit, // 1:1 at creation
		IsPrivate:       isPrivate,
		Name:            name,
		Games:           make(map[uint64]bool),
	}

	c.bankrolls[id] = br

	// Record LP shares.
	if initialDeposit > 0 {
		c.userShares[sharesKey(id, owner)] = initialDeposit
	}

	c.emit("bankroll_created",
		"bankroll_id", u64(id),
		"owner", owner,
		"deposit", u64(initialDeposit),
		"is_private", fmt.Sprintf("%t", isPrivate),
	)

	return id, nil
}

// DepositBankroll adds USDC to a bankroll. Mints shares proportionally.
// Mirrors MsgDeposit.
func (c *Chain) DepositBankroll(bankrollID uint64, depositor string, amount uint64) (sharesMinted uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return 0, fmt.Errorf("bankroll %d not found", bankrollID)
	}

	if br.IsPrivate && br.Creator != depositor {
		return 0, fmt.Errorf("only creator can deposit into a private bankroll")
	}

	if amount == 0 {
		return 0, fmt.Errorf("amount must be > 0")
	}

	// Min deposit check.
	if c.params.MinDepositAmount > 0 && amount < c.params.MinDepositAmount {
		return 0, fmt.Errorf("deposit too small: %d < %d", amount, c.params.MinDepositAmount)
	}

	// Debit depositor.
	acc, ok := c.accounts[depositor]
	if !ok {
		return 0, fmt.Errorf("unknown address %s", depositor)
	}
	if acc.Balance < amount {
		return 0, fmt.Errorf("insufficient balance: %d < %d", acc.Balance, amount)
	}
	acc.Balance -= amount

	// Mint shares: shares = amount * totalShares / balance.
	// If balance is 0, shares = amount (1:1).
	if br.Balance == 0 || br.TotalShares == 0 {
		sharesMinted = amount
	} else {
		sharesMinted = amount * br.TotalShares / br.Balance
	}
	if sharesMinted == 0 {
		// Refund.
		acc.Balance += amount
		return 0, fmt.Errorf("deposit too small to mint shares")
	}

	br.Balance += amount
	br.TotalShares += sharesMinted

	key := sharesKey(bankrollID, depositor)
	c.userShares[key] += sharesMinted

	c.emit("deposit",
		"bankroll_id", u64(bankrollID),
		"depositor", depositor,
		"amount", u64(amount),
		"shares_minted", u64(sharesMinted),
	)

	return sharesMinted, nil
}

// WithdrawBankroll requests a withdrawal by burning shares.
// In the real chain, withdrawals are time-delayed. Here, instant.
// Mirrors MsgWithdraw.
func (c *Chain) WithdrawBankroll(bankrollID uint64, withdrawer string, shares uint64) (amountOut uint64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return 0, fmt.Errorf("bankroll %d not found", bankrollID)
	}

	if br.IsPrivate && br.Creator != withdrawer {
		return 0, fmt.Errorf("only creator can withdraw from a private bankroll")
	}

	key := sharesKey(bankrollID, withdrawer)
	held := c.userShares[key]
	if held < shares {
		return 0, fmt.Errorf("insufficient shares: %d < %d", held, shares)
	}

	// Compute amount: amount = shares * balance / totalShares.
	if br.TotalShares == 0 {
		return 0, fmt.Errorf("no shares outstanding")
	}
	amountOut = shares * br.Balance / br.TotalShares

	// Check available liquidity (can't withdraw reserved funds).
	available := br.Available()
	if amountOut > available {
		return 0, fmt.Errorf("insufficient available liquidity: %d available, %d requested", available, amountOut)
	}

	// Burn shares, debit bankroll, credit withdrawer.
	c.userShares[key] -= shares
	br.TotalShares -= shares
	br.Balance -= amountOut

	acc, ok := c.accounts[withdrawer]
	if !ok {
		acc = &Account{Address: withdrawer}
		c.accounts[withdrawer] = acc
	}
	acc.Balance += amountOut

	return amountOut, nil
}

// AttachGame enables a game on a bankroll.
// Mirrors MsgBankrollAddGame.
func (c *Chain) AttachGame(bankrollID, calcID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return fmt.Errorf("bankroll %d not found", bankrollID)
	}

	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	if !calc.Active {
		return fmt.Errorf("calculator %d is inactive", calcID)
	}

	br.Games[calcID] = true
	return nil
}

// DetachGame disables a game on a bankroll.
func (c *Chain) DetachGame(bankrollID, calcID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return fmt.Errorf("bankroll %d not found", bankrollID)
	}

	delete(br.Games, calcID)
	return nil
}

// GetBankroll returns bankroll info.
func (c *Chain) GetBankroll(bankrollID uint64) (*Bankroll, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return nil, fmt.Errorf("bankroll %d not found", bankrollID)
	}
	cp := *br
	return &cp, nil
}

// ListBankrolls returns all bankrolls.
func (c *Chain) ListBankrolls() []*Bankroll {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]*Bankroll, 0, len(c.bankrolls))
	for _, br := range c.bankrolls {
		cp := *br
		out = append(out, &cp)
	}
	return out
}

// GetUserShares returns LP shares for an address in a bankroll.
func (c *Chain) GetUserShares(bankrollID uint64, addr string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.userShares[sharesKey(bankrollID, addr)]
}
