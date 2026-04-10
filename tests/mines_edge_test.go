// Mines house edge test — 50K bets, 3 mines, reveal tiles 0-4 then cashout.
// Expected edge: ~2% (configured houseEdgeBP=200).
package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestMinesHouseEdge(t *testing.T) {
	params := chainsim.DefaultParams()
	chain := chainsim.NewWithParams(params, 7777)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, err := os.ReadFile("../wasm/mines.wasm")
	if err != nil {
		t.Fatalf("read mines.wasm: %v", err)
	}
	chain.RegisterGame(1, wasmBytes, "mines", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	chain.Deposit("player", 1_000_000_000_000)

	totalStaked := uint64(0)
	totalPayout := uint64(0)
	betsPlaced := 0
	blocks := 0

	betParams := []byte{3} // 3 mines

	const totalBets = 50000

	for betsPlaced < totalBets {
		bal, _ := chain.Balance("player")
		if bal < 1_000_000 {
			chain.Deposit("player", 1_000_000_000_000)
		}

		betID, err := chain.PlaceBet("player", brID, 1, 1_000_000, betParams)
		if err != nil {
			chain.AdvanceBlock()
			blocks++
			continue
		}
		betsPlaced++
		totalStaked += 1_000_000

		// Reveal tiles 0,1,2,3,4 sequentially.
		settled := false
		for tile := byte(0); tile < 5 && !settled; tile++ {
			err := chain.BetAction("player", betID, []byte{1, tile})
			if err != nil {
				settled = true
				break
			}

			result := chain.AdvanceBlock()
			blocks++

			for _, s := range result.Settlements {
				if s.BetID == betID {
					totalPayout += s.Payout
					settled = true
				}
			}
		}

		// Cashout if survived all reveals.
		if !settled {
			err := chain.BetAction("player", betID, []byte{2})
			if err == nil {
				result := chain.AdvanceBlock()
				blocks++
				for _, s := range result.Settlements {
					if s.BetID == betID {
						totalPayout += s.Payout
						settled = true
					}
				}
			}
		}

		// Wait for timeout settlement if needed.
		for !settled {
			result := chain.AdvanceBlock()
			blocks++
			for _, s := range result.Settlements {
				if s.BetID == betID {
					totalPayout += s.Payout
					settled = true
				}
			}
			if blocks > 10_000_000 {
				t.Fatalf("stuck at bet %d", betsPlaced)
			}
		}

		if betsPlaced%10000 == 0 {
			profit := int64(totalStaked) - int64(totalPayout)
			edge := float64(profit) / float64(totalStaked) * 100
			kvUsage := chain.KVUsage(1)
			fmt.Fprintf(os.Stderr, "  %dk bets: edge=%.2f%% kv=%d blocks=%d\n",
				betsPlaced/1000, edge, kvUsage, blocks)
		}
	}

	profit := int64(totalStaked) - int64(totalPayout)
	edge := float64(profit) / float64(totalStaked) * 100
	kvUsage := chain.KVUsage(1)

	fmt.Fprintf(os.Stderr, "\n=== MINES %dK — 3 mines, reveal 5 ===\nBets: %d | Edge: %.2f%% | KV: %d bytes\n",
		totalBets/1000, betsPlaced, edge, kvUsage)

	if edge < 0.0 || edge > 4.0 {
		t.Errorf("house edge %.2f%% outside expected range [0.0%%, 4.0%%]", edge)
	}
}
