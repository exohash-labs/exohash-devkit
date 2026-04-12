package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashKill1000Players(t *testing.T) {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, _ := os.ReadFile("../wasm/crash.wasm")
	chain.RegisterGame(1, wasmBytes, "crash", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	const numPlayers = 1000
	for i := 0; i < numPlayers; i++ {
		chain.Deposit(fmt.Sprintf("p%d", i), 1_000_000_000_000)
	}

	fmt.Fprintf(os.Stderr, "\n=== CRASH KILL TEST: %d players ===\n\n", numPlayers)
	fmt.Fprintf(os.Stderr, "  initial gas balance: %d\n", chain.GasBalance(1))

	// Advance to open phase.
	for b := 0; b < 5; b++ {
		chain.AdvanceBlock()
	}

	// Place all bets.
	placed := 0
	for i := 0; i < numPlayers; i++ {
		_, err := chain.PlaceBet(fmt.Sprintf("p%d", i), brID, 1, 1_000_000, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  place_bet failed at player %d: %v\n", i, err)
			break
		}
		placed++
	}
	fmt.Fprintf(os.Stderr, "  placed %d bets\n", placed)
	fmt.Fprintf(os.Stderr, "  gas balance after bets: %d\n", chain.GasBalance(1))

	// Advance blocks until crash settles or game dies.
	for b := 0; b < 200; b++ {
		result := chain.AdvanceBlock()

		phase := ""
		for _, ev := range result.CalcEvents {
			if ev.Topic == "state" {
				var s struct{ Phase string `json:"phase"` }
				json.Unmarshal([]byte(ev.Data), &s)
				phase = s.Phase
			}
		}

		// Check for kill event.
		for _, ev := range result.CalcEvents {
			_ = ev
		}

		// Check chain events for kill.
		for _, ev := range chain.DrainEvents() {
			if ev.Type == "calculator_killed" {
				fmt.Fprintf(os.Stderr, "\n  *** CALCULATOR KILLED ***\n")
				fmt.Fprintf(os.Stderr, "  block:       %d\n", chain.Height())
				fmt.Fprintf(os.Stderr, "  reason:      %s\n", ev.Attrs["reason"])
				fmt.Fprintf(os.Stderr, "  calculator:  %s\n", ev.Attrs["calculator_id"])
				fmt.Fprintf(os.Stderr, "  gas balance: %d\n", chain.GasBalance(1))

				wins, losses, refunds := 0, 0, 0
				for _, s := range result.Settlements {
					switch s.Kind {
					case 1: wins++
					case 2: losses++
					case 3: refunds++
					}
				}
				fmt.Fprintf(os.Stderr, "  settlements: %d (wins=%d losses=%d refunds=%d)\n",
					len(result.Settlements), wins, losses, refunds)
				fmt.Fprintf(os.Stderr, "\n")
				return
			}
		}

		calc, _ := chain.GetCalculator(1)
		if calc.Status != chainsim.CalcStatusActive {
			fmt.Fprintf(os.Stderr, "  killed but no event found\n")
			return
		}

		if len(result.Settlements) > 0 {
			fmt.Fprintf(os.Stderr, "  block %d: %d settlements, phase=%s\n", chain.Height(), len(result.Settlements), phase)
		}
	}

	fmt.Fprintf(os.Stderr, "  game survived 200 blocks (not killed)\n")
	fmt.Fprintf(os.Stderr, "  gas balance: %d\n\n", chain.GasBalance(1))
}
