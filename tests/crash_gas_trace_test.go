package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashGasTrace(t *testing.T) {
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
	seenFirstOpen := false

	fmt.Fprintf(os.Stderr, "\n%-6s %-10s %10s  %s\n", "block", "phase", "gas", "action")
	fmt.Fprintf(os.Stderr, "%s\n", "--------------------------------------------------------------")

	for blocks := 0; blocks < 200; blocks++ {
		gasBefore := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		gasAfter := chain.WasmGasUsed(1)
		var blockGas uint64
		if gasAfter >= gasBefore {
			blockGas = gasAfter - gasBefore
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

		action := "block_update"
		if len(result.Settlements) > 0 {
			for _, s := range result.Settlements {
				kind := "loss"
				if s.Kind == 1 { kind = "win" }
				if s.Kind == 3 { kind = "refund" }
				action = fmt.Sprintf("block_update → settle bet=%d payout=%d %s", s.BetID, s.Payout, kind)
			}
		}

		fmt.Fprintf(os.Stderr, "%-6d %-10s %10d  %s\n", chain.Height(), phase, blockGas, action)

		// Place bet on first open after init.
		if phase == "open" && betID == 0 && seenFirstOpen {
			gasBefore = chain.WasmGasUsed(1)
			id, err := chain.PlaceBet("player", brID, 1, 1_000_000, nil)
			gasAfter = chain.WasmGasUsed(1)
			var g uint64
			if gasAfter >= gasBefore { g = gasAfter - gasBefore }
			if err == nil {
				betID = id
				fmt.Fprintf(os.Stderr, "%-6s %-10s %10d  place_bet id=%d\n", "", "", g, betID)
			}
		}
		if phase == "open" {
			seenFirstOpen = true
		}

		// Cashout at 1.5x.
		if phase == "tick" && betID > 0 && !cashoutSent && multBP >= 15000 {
			gasBefore = chain.WasmGasUsed(1)
			chain.BetAction("player", betID, []byte{1})
			gasAfter = chain.WasmGasUsed(1)
			var g uint64
			if gasAfter >= gasBefore { g = gasAfter - gasBefore }
			fmt.Fprintf(os.Stderr, "%-6s %-10s %10d  bet_action cashout id=%d\n", "", "", g, betID)
			cashoutSent = true
		}

		// Stop after we see the next open (full cycle).
		if phase == "open" && betID > 0 && cashoutSent {
			break
		}
		if phase == "crashed" {
			betID = 0
			cashoutSent = false
		}
	}
	fmt.Fprintf(os.Stderr, "\n")
}
