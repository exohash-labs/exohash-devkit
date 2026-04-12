package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashTotalGas(t *testing.T) {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, _ := os.ReadFile("../wasm/crash.wasm")
	chain.RegisterGame(1, wasmBytes, "crash", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	const numPlayers = 100
	for i := 0; i < numPlayers; i++ {
		chain.Deposit(fmt.Sprintf("p%d", i), 1_000_000_000_000)
	}

	activeBets := make(map[int]uint64)
	roundsDone := 0
	blocks := 0
	betsJoined := false
	cashoutSent := false
	prevPhase := ""

	var totalPlaceBetGas uint64
	var totalBetActionGas uint64
	var totalBlockUpdateGas uint64
	var placeBetCalls int
	var betActionCalls int

	for roundsDone < 100 {
		// block_update
		wBefore := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		wAfter := chain.WasmGasUsed(1)
		blocks++
		if wAfter >= wBefore {
			totalBlockUpdateGas += wAfter - wBefore
		}

		phase := ""
		var multBP uint64
		for _, ev := range result.CalcEvents {
			if ev.Topic == "state" {
				var s struct {
					Phase  string `json:"phase"`
					MultBP uint64 `json:"mult_bp"`
				}
				json.Unmarshal([]byte(ev.Data), &s)
				phase = s.Phase
				multBP = s.MultBP
			}
		}

		// Round boundary: transition from crashed → open.
		if phase == "open" && prevPhase == "crashed" && betsJoined {
			roundsDone++
			betsJoined = false
			cashoutSent = false
			activeBets = make(map[int]uint64)
			if roundsDone%20 == 0 {
				fmt.Fprintf(os.Stderr, "  round %d done, blocks=%d\n", roundsDone, blocks)
			}
		}
		prevPhase = phase

		// Place bets during open.
		if phase == "open" && !betsJoined {
			for i := 0; i < numPlayers; i++ {
				addr := fmt.Sprintf("p%d", i)
				bal, _ := chain.Balance(addr)
				if bal < 1_000_000 {
					chain.Deposit(addr, 1_000_000_000_000)
				}
				wB := chain.WasmGasUsed(1)
				betID, err := chain.PlaceBet(addr, brID, 1, 1_000_000, nil)
				wA := chain.WasmGasUsed(1)
				if err == nil {
					activeBets[i] = betID
					if wA >= wB {
						totalPlaceBetGas += wA - wB
						placeBetCalls++
					}
				}
			}
			betsJoined = true
		}

		// Cashout at 1.5x.
		if phase == "tick" && !cashoutSent && multBP >= 15000 {
			for i, betID := range activeBets {
				addr := fmt.Sprintf("p%d", i)
				wB := chain.WasmGasUsed(1)
				chain.BetAction(addr, betID, []byte{1})
				wA := chain.WasmGasUsed(1)
				if wA >= wB {
					totalBetActionGas += wA - wB
					betActionCalls++
				}
			}
			cashoutSent = true
		}
	}

	totalGas := totalPlaceBetGas + totalBetActionGas + totalBlockUpdateGas

	fmt.Fprintf(os.Stderr, "\n=== CRASH TOTAL GAS: 100 rounds × %d players ===\n\n", numPlayers)
	fmt.Fprintf(os.Stderr, "  blocks:       %d  (avg %.1f per round)\n", blocks, float64(blocks)/100)
	fmt.Fprintf(os.Stderr, "  place_bet:    %12d gas  (%d calls, avg %d)\n", totalPlaceBetGas, placeBetCalls, totalPlaceBetGas/uint64(placeBetCalls))
	fmt.Fprintf(os.Stderr, "  bet_action:   %12d gas  (%d calls, avg %d)\n", totalBetActionGas, betActionCalls, totalBetActionGas/uint64(betActionCalls))
	fmt.Fprintf(os.Stderr, "  block_update: %12d gas  (%d blocks, avg %d)\n", totalBlockUpdateGas, blocks, totalBlockUpdateGas/uint64(blocks))
	fmt.Fprintf(os.Stderr, "  TOTAL:        %12d gas\n", totalGas)
	fmt.Fprintf(os.Stderr, "  per round:    %12d gas\n", totalGas/100)
	fmt.Fprintf(os.Stderr, "  per bet:      %12d gas\n", totalGas/uint64(placeBetCalls))
	fmt.Fprintf(os.Stderr, "\n")
}
