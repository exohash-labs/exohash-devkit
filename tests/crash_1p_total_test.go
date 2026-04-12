package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrash1PlayerTotal(t *testing.T) {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, _ := os.ReadFile("../wasm/crash.wasm")
	chain.RegisterGame(1, wasmBytes, "crash", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)
	chain.Deposit("player", 1_000_000_000_000)

	betID := uint64(0)
	cashoutSent := false
	roundsDone := 0
	blocks := 0
	prevPhase := ""
	betsPlaced := 0

	gasBefore := chain.WasmGasUsed(1)
	balBefore := chain.GasBalance(1)

	for roundsDone < 100 {
		result := chain.AdvanceBlock()
		blocks++

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

		if phase == "open" && prevPhase == "crashed" && betsPlaced > 0 {
			roundsDone++
			betID = 0
			cashoutSent = false
		}
		prevPhase = phase

		// Place bet first open block.
		if phase == "open" && betID == 0 {
			bal, _ := chain.Balance("player")
			if bal < 1_000_000 {
				chain.Deposit("player", 1_000_000_000_000)
			}
			id, err := chain.PlaceBet("player", brID, 1, 1_000_000, nil)
			if err == nil {
				betID = id
				betsPlaced++
				cashoutSent = false
			}
		}

		// Cashout at 1.5x.
		if phase == "tick" && betID > 0 && !cashoutSent && multBP >= 15000 {
			chain.BetAction("player", betID, []byte{1})
			cashoutSent = true
		}
	}

	gasAfter := chain.WasmGasUsed(1)
	balAfter := chain.GasBalance(1)

	var totalGas uint64
	if gasAfter >= gasBefore {
		totalGas = gasAfter - gasBefore
	}

	fmt.Fprintf(os.Stderr, "\n=== CRASH 1 PLAYER × 100 ROUNDS ===\n\n")
	fmt.Fprintf(os.Stderr, "  blocks:       %d  (avg %.1f per round)\n", blocks, float64(blocks)/100)
	fmt.Fprintf(os.Stderr, "  bets placed:  %d\n", betsPlaced)
	fmt.Fprintf(os.Stderr, "  total gas:    %d\n", totalGas)
	fmt.Fprintf(os.Stderr, "  per round:    %d\n", totalGas/100)
	fmt.Fprintf(os.Stderr, "  per bet:      %d\n", totalGas/uint64(betsPlaced))
	fmt.Fprintf(os.Stderr, "  gas balance:  %d → %d  (delta %d)\n", balBefore, balAfter, int64(balAfter)-int64(balBefore))
	fmt.Fprintf(os.Stderr, "  credits in:   %d  (bets × 1M)\n", uint64(betsPlaced)*chainsim.GasCreditPerBet)
	fmt.Fprintf(os.Stderr, "  gas burned:   %d\n", totalGas)
	fmt.Fprintf(os.Stderr, "  net per round: %d\n", (int64(uint64(betsPlaced)*chainsim.GasCreditPerBet)-int64(totalGas))/100)
	fmt.Fprintf(os.Stderr, "\n")
}
