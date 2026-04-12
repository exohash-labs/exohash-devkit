package tests

import (
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestMinesTotalGas(t *testing.T) {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 7777)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, _ := os.ReadFile("../wasm/mines.wasm")
	chain.RegisterGame(1, wasmBytes, "mines", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)
	chain.Deposit("player", 1_000_000_000_000)

	var totalPlaceBet, totalBetAction, totalBlockUpdate uint64
	var placeCalls, actionCalls, blocks int
	bets := 0

	for bets < 100 {
		bal, _ := chain.Balance("player")
		if bal < 1_000_000 {
			chain.Deposit("player", 1_000_000_000_000)
		}

		wB := chain.WasmGasUsed(1)
		betID, err := chain.PlaceBet("player", brID, 1, 1_000_000, []byte{3})
		wA := chain.WasmGasUsed(1)
		if err != nil {
			chain.AdvanceBlock()
			blocks++
			continue
		}
		if wA >= wB {
			totalPlaceBet += wA - wB
			placeCalls++
		}
		bets++

		settled := false
		for tile := byte(0); tile < 5 && !settled; tile++ {
			wB = chain.WasmGasUsed(1)
			err := chain.BetAction("player", betID, []byte{1, tile})
			wA = chain.WasmGasUsed(1)
			if err != nil {
				settled = true
				break
			}
			if wA >= wB {
				totalBetAction += wA - wB
				actionCalls++
			}

			wB = chain.WasmGasUsed(1)
			result := chain.AdvanceBlock()
			wA = chain.WasmGasUsed(1)
			blocks++
			if wA >= wB {
				totalBlockUpdate += wA - wB
			}

			for _, s := range result.Settlements {
				if s.BetID == betID {
					settled = true
				}
			}
		}

		if !settled {
			wB = chain.WasmGasUsed(1)
			chain.BetAction("player", betID, []byte{2})
			wA = chain.WasmGasUsed(1)
			if wA >= wB {
				totalBetAction += wA - wB
				actionCalls++
			}

			wB = chain.WasmGasUsed(1)
			chain.AdvanceBlock()
			wA = chain.WasmGasUsed(1)
			blocks++
			if wA >= wB {
				totalBlockUpdate += wA - wB
			}
		}
	}

	total := totalPlaceBet + totalBetAction + totalBlockUpdate

	fmt.Fprintf(os.Stderr, "\n=== MINES TOTAL GAS: 100 bets, 3 mines, 5 reveals ===\n\n")
	fmt.Fprintf(os.Stderr, "  blocks:       %d\n", blocks)
	fmt.Fprintf(os.Stderr, "  place_bet:    %10d gas  (%d calls, avg %d)\n", totalPlaceBet, placeCalls, totalPlaceBet/uint64(placeCalls))
	fmt.Fprintf(os.Stderr, "  bet_action:   %10d gas  (%d calls, avg %d)\n", totalBetAction, actionCalls, totalBetAction/uint64(actionCalls))
	fmt.Fprintf(os.Stderr, "  block_update: %10d gas  (%d blocks, avg %d)\n", totalBlockUpdate, blocks, totalBlockUpdate/uint64(blocks))
	fmt.Fprintf(os.Stderr, "  TOTAL:        %10d gas\n", total)
	fmt.Fprintf(os.Stderr, "  per bet:      %10d gas\n", total/100)
	fmt.Fprintf(os.Stderr, "\n")
}
