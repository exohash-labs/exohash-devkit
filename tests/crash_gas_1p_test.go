package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashGas1Player(t *testing.T) {
	params := chainsim.DefaultParams()
	chain := chainsim.NewWithParams(params, 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, err := os.ReadFile("../wasm/crash.wasm")
	if err != nil {
		t.Fatalf("read crash.wasm: %v", err)
	}
	chain.RegisterGame(1, wasmBytes, "crash", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	chain.Deposit("player", 1_000_000_000_000)

	activeBetID := uint64(0)
	roundsDone := 0
	phase := "waiting"

	for roundsDone < 3 {
		gasBefore := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		gasAfter := chain.WasmGasUsed(1)
		blockGas := gasAfter - gasBefore

		newPhase := phase
		var multBP uint64
		for _, ev := range result.CalcEvents {
			if ev.Topic == "state" {
				var state struct {
					Phase  string `json:"phase"`
					MultBP uint64 `json:"mult_bp"`
				}
				json.Unmarshal([]byte(ev.Data), &state)
				newPhase = state.Phase
				multBP = state.MultBP
			}
		}

		settled := ""
		for _, s := range result.Settlements {
			settled = fmt.Sprintf(" SETTLED betID=%d payout=%d kind=%d", s.BetID, s.Payout, s.Kind)
			activeBetID = 0
		}

		if blockGas > 0 || newPhase != phase || settled != "" {
			fmt.Fprintf(os.Stderr, "  block %-4d phase=%-8s mult=%-6d gas=%-8d%s\n",
				result.Block.Height, newPhase, multBP, blockGas, settled)
		}

		phase = newPhase

		if newPhase == "crashed" {
			activeBetID = 0
			roundsDone++
			phase = "waiting"
			fmt.Fprintf(os.Stderr, "  --- round %d done ---\n\n", roundsDone)
			continue
		}

		// Place bet during open phase.
		if newPhase == "open" && activeBetID == 0 {
			gbefore := chain.WasmGasUsed(1)
			betID, err := chain.PlaceBet("player", brID, 1, 1_000_000, nil)
			gafter := chain.WasmGasUsed(1)
			if err == nil {
				activeBetID = betID
				fmt.Fprintf(os.Stderr, "  PLACE_BET betID=%d gas=%d\n", betID, gafter-gbefore)
			}
		}

		// Cashout at 1.5x.
		if newPhase == "tick" && activeBetID > 0 && multBP >= 15000 {
			gbefore := chain.WasmGasUsed(1)
			chain.BetAction("player", activeBetID, []byte{1})
			gafter := chain.WasmGasUsed(1)
			fmt.Fprintf(os.Stderr, "  CASHOUT betID=%d gas=%d\n", activeBetID, gafter-gbefore)
		}
	}
}
