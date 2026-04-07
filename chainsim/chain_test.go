package chainsim

import (
	"testing"
)

func TestFullLifecycle(t *testing.T) {
	c := New()

	// Register a dice game.
	if err := c.RegisterCalculator(Calculator{ID: 1, Name: "dice", Engine: "dice", HouseEdgeBp: 200}); err != nil {
		t.Fatal(err)
	}

	// LP deposits funds.
	if err := c.Deposit("lp1", 1_000_000_000); err != nil {
		t.Fatal(err)
	}

	// Create bankroll.
	brID, err := c.CreateBankroll("lp1", 1_000_000_000, "Test Bankroll", false)
	if err != nil {
		t.Fatal(err)
	}

	// Attach game.
	if err := c.AttachGame(brID, 1); err != nil {
		t.Fatal(err)
	}

	// Player deposits.
	if err := c.Deposit("player1", 100_000_000); err != nil {
		t.Fatal(err)
	}

	// Place bet.
	betID, err := c.PlaceBet("player1", brID, 1, 1_000_000, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Check player balance decreased.
	bal, _ := c.Balance("player1")
	if bal != 99_000_000 {
		t.Fatalf("expected 99M, got %d", bal)
	}

	// Reserve (WASM would call this during place_bet).
	c.SetMode(CalcModePlaceBet)
	if err := c.Reserve(betID, 2_000_000); err != nil {
		t.Fatal(err)
	}

	// Check bankroll reserved.
	br, _ := c.GetBankroll(brID)
	if br.TotalReserved != 2_000_000 {
		t.Fatalf("expected 2M reserved, got %d", br.TotalReserved)
	}

	// Settle as win — payout 1.95M (player wins).
	c.SetMode(CalcModeBlockUpdate)
	if err := c.Settle(betID, 1_950_000, 1); err != nil {
		t.Fatal(err)
	}

	// Check bet settled.
	bet := c.GetBet(betID)
	if bet.Status != BetSettled {
		t.Fatalf("expected settled, got %s", bet.Status)
	}
	if bet.Payout != 1_950_000 {
		t.Fatalf("expected payout 1.95M, got %d", bet.Payout)
	}

	// Check player got paid.
	bal, _ = c.Balance("player1")
	if bal != 99_000_000+1_950_000 {
		t.Fatalf("expected 100.95M, got %d", bal)
	}

	// Check bankroll reserved released.
	br, _ = c.GetBankroll(brID)
	if br.TotalReserved != 0 {
		t.Fatalf("expected 0 reserved, got %d", br.TotalReserved)
	}

	// Check fees collected.
	if c.TotalValFees == 0 || c.TotalProtoFees == 0 {
		t.Fatalf("expected fees collected: val=%d proto=%d", c.TotalValFees, c.TotalProtoFees)
	}

	t.Logf("Fees: val=%d proto=%d", c.TotalValFees, c.TotalProtoFees)
	t.Logf("Bankroll balance: %d", br.Balance)
	t.Logf("Player balance: %d", bal)
}

func TestSolvencyReject(t *testing.T) {
	c := New()

	c.RegisterCalculator(Calculator{ID: 1, Name: "dice", Engine: "dice", HouseEdgeBp: 200})
	c.Deposit("lp1", 100_000) // tiny bankroll
	brID, _ := c.CreateBankroll("lp1", 100_000, "Tiny", false)
	c.AttachGame(brID, 1)
	c.Deposit("player1", 1_000_000_000)

	// Place bet — should succeed.
	betID, err := c.PlaceBet("player1", brID, 1, 1_000, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Reserve more than MaxPayoutCapBps allows (2% of 100K = 2K).
	c.SetMode(CalcModePlaceBet)
	err = c.Reserve(betID, 3_000)
	if err == nil {
		t.Fatal("expected reserve to fail — exceeds MaxPayoutCapBps")
	}
	t.Logf("Correctly rejected: %v", err)

	// Reserve within limits.
	err = c.Reserve(betID, 1_500)
	if err != nil {
		t.Fatalf("expected reserve to succeed: %v", err)
	}
}

func TestGameNotAttached(t *testing.T) {
	c := New()

	c.RegisterCalculator(Calculator{ID: 1, Name: "dice", Engine: "dice", HouseEdgeBp: 200})
	c.Deposit("lp1", 1_000_000)
	brID, _ := c.CreateBankroll("lp1", 1_000_000, "No Games", false)
	// NOT attaching game.
	c.Deposit("player1", 100_000)

	_, err := c.PlaceBet("player1", brID, 1, 1_000, nil)
	if err == nil {
		t.Fatal("expected bet to fail — game not attached")
	}
	t.Logf("Correctly rejected: %v", err)
}
