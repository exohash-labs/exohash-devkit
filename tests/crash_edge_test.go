// Crash house edge test — 50K bets, cashout at 1.5x.
// Expected edge: ~1.9% (2% base + crash-before-cashout risk).
package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashHouseEdge(t *testing.T) {
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

	totalStaked := uint64(0)
	totalPayout := uint64(0)
	betsPlaced := 0
	activeBetID := uint64(0)
	cashoutTarget := uint64(15000) // 1.5x
	cashoutSent := false
	lastReport := 0
	blocks := 0

	const totalBets = 50000

	for betsPlaced < totalBets || activeBetID > 0 {
		blocks++
		result := chain.AdvanceBlock()

		for _, ev := range result.CalcEvents {
			if ev.Topic == "state" {
				var state struct {
					Phase  string `json:"phase"`
					MultBP uint64 `json:"mult_bp"`
				}
				json.Unmarshal([]byte(ev.Data), &state)

				if state.Phase == "open" && activeBetID == 0 && betsPlaced < totalBets {
					bal, _ := chain.Balance("player")
					if bal < 1_000_000 {
						chain.Deposit("player", 1_000_000_000_000)
					}
					betID, err := chain.PlaceBet("player", brID, 1, 1_000_000, nil)
					if err == nil {
						activeBetID = betID
						totalStaked += 1_000_000
						betsPlaced++
						cashoutSent = false
					}
				}

				if state.Phase == "tick" && activeBetID > 0 && !cashoutSent && state.MultBP >= cashoutTarget {
					chain.BetAction("player", activeBetID, []byte{1})
					cashoutSent = true
				}

				if state.Phase == "crashed" {
					activeBetID = 0
					cashoutSent = false
				}
			}
		}

		for _, s := range result.Settlements {
			totalPayout += s.Payout
			if s.BetID == activeBetID {
				activeBetID = 0
			}
		}

		if betsPlaced > lastReport && betsPlaced%10000 == 0 {
			lastReport = betsPlaced
			profit := int64(totalStaked) - int64(totalPayout)
			fmt.Fprintf(os.Stderr, "  %dk bets: edge=%.2f%% blocks=%d\n",
				betsPlaced/1000, float64(profit)/float64(totalStaked)*100, blocks)
		}
	}

	profit := int64(totalStaked) - int64(totalPayout)
	edge := float64(profit) / float64(totalStaked) * 100

	fmt.Fprintf(os.Stderr, "\n=== CRASH %dK — cashout 1.5x ===\nBets: %d | Edge: %.2f%%\n",
		totalBets/1000, betsPlaced, edge)

	if edge < 0.5 || edge > 4.0 {
		t.Errorf("house edge %.2f%% outside expected range [0.5%%, 4.0%%]", edge)
	}
}
