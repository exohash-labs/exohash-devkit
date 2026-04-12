package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

// TestMinesGasScaling measures per-block gas for mines with varying active bets.
// Mines processes every active bet each block_update (timeout tick + state emit).
func TestMinesGasScaling(t *testing.T) {
	wasmBytes, err := os.ReadFile("../wasm/mines.wasm")
	if err != nil {
		t.Fatalf("read mines.wasm: %v", err)
	}

	for _, numPlayers := range []int{1, 10, 50, 100, 200, 500} {
		t.Run(fmt.Sprintf("%d_players", numPlayers), func(t *testing.T) {
			params := chainsim.DefaultParams()
			chain := chainsim.NewWithParams(params, 42)
			defer chain.Close()

			chain.Deposit("house", 1_000_000_000_000_000)
			brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)
			chain.RegisterGame(1, wasmBytes, "mines", 200)
			chain.AttachGame(brID, 1)
			chain.InitGame(1, brID)

			for i := 0; i < numPlayers; i++ {
				chain.Deposit(fmt.Sprintf("p%d", i), 1_000_000_000_000)
			}

			// Place all bets (3 mines each).
			for i := 0; i < numPlayers; i++ {
				addr := fmt.Sprintf("p%d", i)
				_, err := chain.PlaceBet(addr, brID, 1, 1_000_000, []byte{3})
				if err != nil {
					t.Fatalf("place bet %d: %v", i, err)
				}
			}

			// Advance a few blocks to measure tick cost (all bets active, no reveals).
			var tickGases []uint64
			for b := 0; b < 5; b++ {
				gasBefore := chain.WasmGasUsed(1)
				chain.AdvanceBlock()
				gasAfter := chain.WasmGasUsed(1)
				tickGases = append(tickGases, gasAfter-gasBefore)
			}

			// Now have all players reveal tile 0 (triggers WaitingRNG).
			for i := 0; i < numPlayers; i++ {
				betID := uint64(i + 2) // bet IDs start at 2 (sentinel is 1)
				chain.BetAction(fmt.Sprintf("p%d", i), betID, []byte{1, 0}) // reveal tile 0
			}

			// Advance one block — resolves all reveals (SHA-256 + settle/continue per bet).
			gasBefore := chain.WasmGasUsed(1)
			result := chain.AdvanceBlock()
			gasAfter := chain.WasmGasUsed(1)
			resolveGas := gasAfter - gasBefore
			settled := len(result.Settlements)

			minTick, maxTick := tickGases[0], tickGases[0]
			for _, g := range tickGases {
				if g < minTick { minTick = g }
				if g > maxTick { maxTick = g }
			}

			fmt.Fprintf(os.Stderr, "  %4d bets: tick=%8d-%8d  resolve=%10d  settled=%d\n",
				numPlayers, minTick, maxTick, resolveGas, settled)
		})
	}
}
