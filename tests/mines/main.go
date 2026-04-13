// Mines simulation — permanent players, fixed rounds, simultaneous play.
//
// Each round: all players place a bet (3 mines), reveal tiles 0,1,2,
// survivors cashout. Tracks house edge, gas per operation, memory, KV.
//
// Run:  go run ./tests/mines
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

const (
	rounds     = 10_000
	players    = 5
	stake      = 1_000_000 // 1 USDC per bet
	minesCount = 3
	reveals    = 3
)

func main() {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, err := os.ReadFile("wasm/mines.wasm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read mines.wasm: %v\n", err)
		os.Exit(1)
	}
	chain.RegisterGame(1, wasmBytes, "mines", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	addrs := make([]string, players)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("p%d", i)
		chain.Deposit(addrs[i], 1_000_000_000_000)
	}

	var (
		totalStaked      uint64
		totalPayout      uint64
		totalPlaceBetGas uint64
		totalRevealGas   uint64
		totalCashoutGas  uint64
		totalBlockUpdGas uint64
		placeBetCalls    int
		revealCalls      int
		cashoutCalls     int
		blocks           int
		settledWin       int
		settledLoss      int
	)

	tStart := time.Now()

	for round := 1; round <= rounds; round++ {
		// 1. All players place a bet.
		activeBets := make(map[int]uint64)
		for i, addr := range addrs {
			bal, _ := chain.Balance(addr)
			if bal < stake {
				chain.Deposit(addr, 1_000_000_000_000)
			}

			gB := chain.WasmGasUsed(1)
			betID, err := chain.PlaceBet(addr, brID, 1, stake, []byte{minesCount})
			gA := chain.WasmGasUsed(1)
			if err != nil {
				continue
			}
			activeBets[i] = betID
			placeBetCalls++
			totalStaked += stake
			if gA >= gB {
				totalPlaceBetGas += gA - gB
			}
		}

		// 2. Reveal tiles.
		for tile := byte(0); tile < reveals; tile++ {
			for i, betID := range activeBets {
				gB := chain.WasmGasUsed(1)
				err := chain.BetAction(addrs[i], betID, []byte{1, tile})
				gA := chain.WasmGasUsed(1)
				if err != nil {
					delete(activeBets, i)
					continue
				}
				revealCalls++
				if gA >= gB {
					totalRevealGas += gA - gB
				}
			}

			gB := chain.WasmGasUsed(1)
			result := chain.AdvanceBlock()
			gA := chain.WasmGasUsed(1)
			blocks++
			if gA >= gB {
				totalBlockUpdGas += gA - gB
			}

			for _, s := range result.Settlements {
				totalPayout += s.Payout
				if s.Kind == 1 {
					settledWin++
				} else {
					settledLoss++
				}
				for i, betID := range activeBets {
					if betID == s.BetID {
						delete(activeBets, i)
						break
					}
				}
			}
		}

		// 3. Survivors cashout.
		for i, betID := range activeBets {
			gB := chain.WasmGasUsed(1)
			err := chain.BetAction(addrs[i], betID, []byte{2})
			gA := chain.WasmGasUsed(1)
			if err != nil {
				continue
			}
			cashoutCalls++
			if gA >= gB {
				totalCashoutGas += gA - gB
			}
		}

		gB := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		gA := chain.WasmGasUsed(1)
		blocks++
		if gA >= gB {
			totalBlockUpdGas += gA - gB
		}

		for _, s := range result.Settlements {
			totalPayout += s.Payout
			if s.Kind == 1 {
				settledWin++
			} else {
				settledLoss++
			}
		}

		if round%1_000 == 0 {
			chain.PurgeSettledBets()
		}

		if round%1_000 == 0 {
			elapsed := time.Since(tStart)
			profit := int64(totalStaked) - int64(totalPayout)
			edge := float64(profit) / float64(totalStaked) * 100
			fmt.Printf("  round %5d/%d  bets=%-8d  edge=%5.2f%%  elapsed=%s\n",
				round, rounds, placeBetCalls, edge, elapsed.Round(time.Millisecond))
		}
	}

	elapsed := time.Since(tStart)

	totalGas := totalPlaceBetGas + totalRevealGas + totalCashoutGas + totalBlockUpdGas
	profit := int64(totalStaked) - int64(totalPayout)
	edge := float64(profit) / float64(totalStaked) * 100
	kvUsage := chain.KVUsage(1)
	wasmMem := chain.WasmMemorySize(1)
	recycled := chainsim.WasmRecycleCount()
	gasBalance := chain.GasBalance(1)

	fmt.Printf(`
MINES — %d players × %d rounds (%d mines, %d reveals)
═══════════════════════════════════════

Bets placed:    %d
Settled:        win=%d  loss=%d
Total staked:   %d uUSDC
Total payout:   %d uUSDC
House profit:   %d uUSDC
House edge:     %.2f%%

GAS BREAKDOWN
  place_bet:    %12d  (%d calls, avg %d)
  reveal:       %12d  (%d calls, avg %d)
  cashout:      %12d  (%d calls, avg %d)
  block_update: %12d  (%d blocks, avg %d)
  TOTAL:        %12d
  per round:    %12d
  per bet:      %12d

RESOURCES
  KV usage:     %d bytes
  WASM memory:  %d KB
  WASM recycles:%d
  Gas balance:  %d
  Blocks:       %d
  Elapsed:      %s
`,
		players, rounds, minesCount, reveals,
		placeBetCalls,
		settledWin, settledLoss,
		totalStaked, totalPayout, profit, edge,
		totalPlaceBetGas, placeBetCalls, safeDiv(totalPlaceBetGas, uint64(placeBetCalls)),
		totalRevealGas, revealCalls, safeDiv(totalRevealGas, uint64(revealCalls)),
		totalCashoutGas, cashoutCalls, safeDiv(totalCashoutGas, uint64(cashoutCalls)),
		totalBlockUpdGas, blocks, safeDiv(totalBlockUpdGas, uint64(blocks)),
		totalGas,
		safeDiv(totalGas, uint64(rounds)),
		safeDiv(totalGas, uint64(placeBetCalls)),
		kvUsage, wasmMem/1024, recycled, gasBalance, blocks, elapsed.Round(time.Millisecond),
	)
}

func safeDiv(a, b uint64) uint64 {
	if b == 0 {
		return 0
	}
	return a / b
}
