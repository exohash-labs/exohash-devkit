// Dice house edge test — 50K bets at 50% chance (mode=under, threshold=5000).
// Expected edge: ~2% (configured houseEdgeBP=200).
package tests

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestDiceHouseEdge(t *testing.T) {
	params := chainsim.DefaultParams()
	chain := chainsim.NewWithParams(params, 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, err := os.ReadFile("../wasm/dice.wasm")
	if err != nil {
		t.Fatalf("read dice.wasm: %v", err)
	}
	chain.RegisterGame(1, wasmBytes, "dice", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	chain.Deposit("player", 1_000_000_000_000)

	totalStaked := uint64(0)
	totalPayout := uint64(0)
	betsPlaced := 0
	blocks := 0

	// mode=2 (under), threshold=5000 (50% chance)
	betParams := make([]byte, 9)
	betParams[0] = 2
	binary.LittleEndian.PutUint64(betParams[1:], 5000)

	const totalBets = 50000

	for betsPlaced < totalBets {
		bal, _ := chain.Balance("player")
		if bal < 1_000_000 {
			chain.Deposit("player", 1_000_000_000_000)
		}
		_, err := chain.PlaceBet("player", brID, 1, 1_000_000, betParams)
		if err != nil {
			chain.AdvanceBlock()
			blocks++
			continue
		}
		betsPlaced++
		totalStaked += 1_000_000

		result := chain.AdvanceBlock()
		blocks++

		for _, s := range result.Settlements {
			totalPayout += s.Payout
		}

		if betsPlaced%10000 == 0 {
			profit := int64(totalStaked) - int64(totalPayout)
			edge := float64(profit) / float64(totalStaked) * 100
			kvUsage := chain.KVUsage(1)
			wasmMem := chain.WasmMemorySize(1)
			fmt.Fprintf(os.Stderr, "  %dk bets: edge=%.2f%% kv=%d wasm_mem=%dKB blocks=%d\n",
				betsPlaced/1000, edge, kvUsage, wasmMem/1024, blocks)
		}
	}

	profit := int64(totalStaked) - int64(totalPayout)
	edge := float64(profit) / float64(totalStaked) * 100
	kvUsage := chain.KVUsage(1)

	wasmMem := chain.WasmMemorySize(1)
	recycled := chainsim.WasmRecycleCount()
	fmt.Fprintf(os.Stderr, "\n=== DICE %dK — 50%% chance ===\nBets: %d | Edge: %.2f%% | KV: %d bytes | WASM mem: %dKB | recycled: %d\n",
		totalBets/1000, betsPlaced, edge, kvUsage, wasmMem/1024, recycled)

	if edge < 0.5 || edge > 4.0 {
		t.Errorf("house edge %.2f%% outside expected range [0.5%%, 4.0%%]", edge)
	}
	if kvUsage != 0 {
		t.Errorf("KV usage %d bytes, expected 0 (dice should clean up)", kvUsage)
	}
}
