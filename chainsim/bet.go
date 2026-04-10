package chainsim

import "fmt"

// bpsOf computes amount * bps / 10000.
// Mirrors x/house/keeper/fee_split.go bpsOf.
func bpsOf(amount uint64, bps uint32) uint64 {
	return amount * uint64(bps) / 10000
}

// ComputeFeeSplit computes the flat fee breakdown from wagering volume.
// Mirrors keeper.ComputeFeeSplit.
func (c *Chain) ComputeFeeSplit(stake uint64) FeeSplit {
	feeTotal := bpsOf(stake, c.params.ProtocolFeeBp)
	valFee := bpsOf(stake, c.params.ValFeeBp)
	protoFee := feeTotal - valFee

	net := stake - feeTotal
	return FeeSplit{
		ValFee:      valFee,
		ProtoFee:    protoFee,
		BankrollNet: net,
	}
}

// PlaceBet validates solvency, escrows stake, creates a Bet, and executes WASM place_bet.
// Returns the betID. Mirrors msg_server_place_bet.go placeBetV3.
func (c *Chain) PlaceBet(addr string, bankrollID, calcID, stake uint64, params []byte) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mode = CalcModePlaceBet

	betID := c.nextBetID
	c.nextBetID++
	if err := c.placeBetLocked(betID, addr, bankrollID, calcID, stake, params); err != nil {
		return 0, err
	}

	// Execute WASM place_bet if game is registered.
	c.activeCalcID = calcID
	if game, ok := c.games[calcID]; ok {
		// Prepend sender address (20 bytes, padded or truncated).
		senderBytes := make([]byte, 20)
		copy(senderBytes, []byte(addr))
		fullParams := append(senderBytes, params...)

		ctx, _, _ := c.wasmCtxForGame(calcID)
		status, err := game.inst.callPlaceBet(ctx, betID, bankrollID, calcID, stake, fullParams)
		c.reinstantiateIfNeeded(calcID)
		if err != nil || status != 0 {
			// WASM rejected — refund.
			c.mode = CalcModeBetAction
			_ = c.settleLocked(betID, 0, 3)
			if err != nil {
				return 0, fmt.Errorf("place_bet: %w", err)
			}
			return 0, fmt.Errorf("place_bet rejected (status=%d)", status)
		}
	}

	return betID, nil
}

// BetAction executes a player action on an open bet via WASM bet_action.
// Mirrors msg_server_bet_action.go.
func (c *Chain) BetAction(addr string, betID uint64, action []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.mode = CalcModeBetAction

	bet, ok := c.bets[betID]
	if !ok {
		return fmt.Errorf("bet %d not found", betID)
	}
	if bet.Bettor != addr {
		return fmt.Errorf("not your bet")
	}

	game, ok := c.games[bet.CalculatorID]
	if !ok {
		return fmt.Errorf("game %d not registered", bet.CalculatorID)
	}

	c.activeCalcID = bet.CalculatorID
	ctx, _, _ := c.wasmCtxForGame(bet.CalculatorID)
	status, err := game.inst.callBetAction(ctx, betID, action)
	c.reinstantiateIfNeeded(bet.CalculatorID)
	if err != nil {
		return fmt.Errorf("bet_action: %w", err)
	}
	if status != 0 {
		return fmt.Errorf("bet_action rejected (status=%d)", status)
	}

	return nil
}

// placeBetLocked is the shared implementation. Caller must hold c.mu.
func (c *Chain) placeBetLocked(betID uint64, addr string, bankrollID, calcID, stake uint64, params []byte) error {
	// 1. Validate stake meets minimum and account balance.
	if stake == 0 {
		return fmt.Errorf("stake must be > 0")
	}
	if c.params.MinStakeUusdc > 0 && stake < c.params.MinStakeUusdc {
		return fmt.Errorf("stake %d below minimum %d", stake, c.params.MinStakeUusdc)
	}
	acc, ok := c.accounts[addr]
	if !ok {
		return fmt.Errorf("unknown address %s", addr)
	}
	if acc.Balance < stake {
		return fmt.Errorf("insufficient balance: %d < %d", acc.Balance, stake)
	}

	// 2. Validate bankroll exists.
	br, ok := c.bankrolls[bankrollID]
	if !ok {
		return fmt.Errorf("bankroll %d not found", bankrollID)
	}

	// 3. Validate game is attached and active.
	if !br.Games[calcID] {
		return fmt.Errorf("game %d not active on bankroll %d", calcID, bankrollID)
	}
	calc, ok := c.calculators[calcID]
	if !ok {
		return fmt.Errorf("calculator %d not found", calcID)
	}
	if calc.Status != CalcStatusActive {
		return fmt.Errorf("calculator %d is not active (status=%d)", calcID, calc.Status)
	}

	// 4. Compute fee split (flat % of wagering volume).
	split := c.ComputeFeeSplit(stake)

	// 5. Escrow full stake from player.
	acc.Balance -= stake

	// 6. Create bet.
	bet := &Bet{
		ID:           betID,
		BankrollID:   bankrollID,
		CalculatorID: calcID,
		Bettor:       addr,
		Stake:        stake,
		ValFee:       split.ValFee,
		ProtoFee:     split.ProtoFee,
		NetStake:     split.BankrollNet,
		Status:       BetOpen,
		EntryState:   params,
	}
	c.bets[betID] = bet
	c.betsByAddr[addr] = append(c.betsByAddr[addr], betID)
	c.betGame[betID] = calcID

	c.emit("bet_created",
		"bet_id", u64(betID),
		"bankroll_id", u64(bankrollID),
		"bettor", addr,
		"stake", u64(stake),
		"calculator_id", u64(calcID),
	)

	return nil
}

// ---------------------------------------------------------------------------
// Locked internal methods — called by WASM host functions, lock already held
// ---------------------------------------------------------------------------

// reserveLocked locks liquidity in the bankroll for a bet's max payout.
// Mode gate: only allowed during CalcModePlaceBet.
func (c *Chain) reserveLocked(betID, maxPayout uint64) error {
	if c.mode != CalcModePlaceBet {
		return fmt.Errorf("reserve not allowed in mode %d", c.mode)
	}

	bet, ok := c.bets[betID]
	if !ok {
		return fmt.Errorf("bet %d not found", betID)
	}

	br, ok := c.bankrolls[bet.BankrollID]
	if !ok {
		return fmt.Errorf("bankroll %d not found", bet.BankrollID)
	}

	capBps := br.MaxPayoutCapBps
	if capBps == 0 {
		capBps = DefaultMaxPayoutCapBps
	}
	maxAllowed := bpsOf(br.Balance, capBps)
	if maxPayout > maxAllowed {
		return fmt.Errorf("max payout %d exceeds cap %d bps of balance %d (limit=%d)",
			maxPayout, capBps, br.Balance, maxAllowed)
	}

	resBps := br.MaxReservedBps
	if resBps == 0 {
		resBps = DefaultMaxReservedBps
	}
	resLimit := bpsOf(br.Balance, resBps)
	if br.TotalReserved+maxPayout > resLimit {
		return fmt.Errorf("total reserved %d + %d exceeds %d bps limit %d",
			br.TotalReserved, maxPayout, resBps, resLimit)
	}

	available := br.Available()
	if maxPayout > available {
		return fmt.Errorf("insufficient available liquidity: %d available, %d needed", available, maxPayout)
	}

	br.TotalReserved += maxPayout
	bet.Reserved += maxPayout

	return nil
}

// settleLocked resolves a bet. Releases reserved, routes fees, pays bettor.
// Mode gate: only allowed during CalcModeBetAction or CalcModeBlockUpdate.
func (c *Chain) settleLocked(betID, payout uint64, kind uint8) error {
	if c.mode != CalcModeBetAction && c.mode != CalcModeBlockUpdate {
		return fmt.Errorf("settle not allowed in mode %d", c.mode)
	}

	bet, ok := c.bets[betID]
	if !ok {
		return fmt.Errorf("bet %d not found", betID)
	}
	if bet.Status != BetOpen {
		return nil // idempotent
	}

	br, ok := c.bankrolls[bet.BankrollID]
	if !ok {
		return fmt.Errorf("bankroll %d not found", bet.BankrollID)
	}

	// Release reserved amount.
	if bet.Reserved <= br.TotalReserved {
		br.TotalReserved -= bet.Reserved
	} else {
		br.TotalReserved = 0
	}

	// Collect fees.
	c.TotalValFees += bet.ValFee
	c.TotalProtoFees += bet.ProtoFee

	// Net stake goes to bankroll.
	br.Balance += bet.NetStake

	// Pay bettor.
	if payout > 0 {
		if payout > br.Balance {
			payout = br.Balance
		}
		br.Balance -= payout

		acc, ok := c.accounts[bet.Bettor]
		if !ok {
			acc = &Account{Address: bet.Bettor}
			c.accounts[bet.Bettor] = acc
		}
		acc.Balance += payout
	}

	// Emit settlement event.
	payoutKind := "loss"
	if payout > 0 {
		payoutKind = "win"
	}
	if kind == SettleKindRefund {
		payoutKind = "refund"
	}
	c.emit("bet_settled",
		"bet_id", u64(betID),
		"bankroll_id", u64(bet.BankrollID),
		"payout", u64(payout),
		"payout_kind", payoutKind,
		"net_stake", u64(bet.NetStake),
	)

	// Record settlement for drain.
	c.settlements = append(c.settlements, Settlement{BetID: betID, CalcID: bet.CalculatorID, Payout: payout, Kind: kind})

	// Update bet status.
	bet.Payout = payout
	bet.Reserved = 0
	switch kind {
	case SettleKindRefund:
		bet.Status = BetRefunded
		c.TotalValFees -= bet.ValFee
		c.TotalProtoFees -= bet.ProtoFee
		br.Balance -= bet.NetStake
		acc := c.accounts[bet.Bettor]
		if acc != nil {
			acc.Balance += bet.Stake
		}
	default:
		bet.Status = BetSettled
	}

	return nil
}

// increaseStakeLocked adds more stake to an open bet.
func (c *Chain) increaseStakeLocked(betID, additional uint64) error {
	if c.mode != CalcModeBetAction && c.mode != CalcModeBlockUpdate {
		return fmt.Errorf("increase_stake not allowed in mode %d", c.mode)
	}

	bet, ok := c.bets[betID]
	if !ok {
		return fmt.Errorf("bet %d not found", betID)
	}

	acc, ok := c.accounts[bet.Bettor]
	if !ok {
		return fmt.Errorf("bettor account not found")
	}
	if acc.Balance < additional {
		return fmt.Errorf("insufficient balance for increase: %d < %d", acc.Balance, additional)
	}
	acc.Balance -= additional

	if bet.Stake > 0 && bet.Reserved > 0 {
		additionalReserved := additional * bet.Reserved / bet.Stake
		br, ok := c.bankrolls[bet.BankrollID]
		if !ok {
			return fmt.Errorf("bankroll %d not found", bet.BankrollID)
		}
		br.TotalReserved += additionalReserved
		bet.Reserved += additionalReserved
	}

	bet.Stake += additional
	return nil
}

// ---------------------------------------------------------------------------
// Public wrappers for host-callback operations (used by tests / external tooling)
// ---------------------------------------------------------------------------

// Reserve locks liquidity. Public wrapper — acquires lock.
func (c *Chain) Reserve(betID, maxPayout uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reserveLocked(betID, maxPayout)
}

// Settle resolves a bet. Public wrapper — acquires lock.
func (c *Chain) Settle(betID, payout uint64, kind uint8) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.settleLocked(betID, payout, kind)
}

// IncreaseStake adds more stake. Public wrapper — acquires lock.
func (c *Chain) IncreaseStake(betID, additional uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.increaseStakeLocked(betID, additional)
}

// GetRNG returns deterministic randomness. Public wrapper — acquires lock.
func (c *Chain) GetRNG(height uint64) []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getRNGLocked(height)
}

// GetBetCount returns wakeup batch size. Public wrapper.
func (c *Chain) GetBetCount() uint32 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getBetCountLocked()
}

// GetBetID returns bet ID at index. Public wrapper.
func (c *Chain) GetBetID(index uint32) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getBetIDLocked(index)
}

// GetPendingAction returns queued action. Public wrapper.
func (c *Chain) GetPendingAction(betID uint64) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getPendingActionLocked(betID)
}

// ---------------------------------------------------------------------------
// Public query methods (unchanged)
// ---------------------------------------------------------------------------

// GetBet returns a bet by ID.
func (c *Chain) GetBet(betID uint64) *Bet {
	c.mu.RLock()
	defer c.mu.RUnlock()

	bet, ok := c.bets[betID]
	if !ok {
		return nil
	}
	cp := *bet
	return &cp
}

// BetHistory returns recent bets for an address.
func (c *Chain) BetHistory(addr string, limit int) []Bet {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := c.betsByAddr[addr]
	if limit <= 0 || limit > len(ids) {
		limit = len(ids)
	}

	out := make([]Bet, 0, limit)
	for i := len(ids) - 1; i >= 0 && len(out) < limit; i-- {
		if bet, ok := c.bets[ids[i]]; ok {
			out = append(out, *bet)
		}
	}
	return out
}
