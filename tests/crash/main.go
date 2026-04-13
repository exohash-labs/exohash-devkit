// Crash simulation — permanent players, fixed rounds, simultaneous play.
//
// Each round: open phase (players join) → tick phase (multiplier rises,
// players cashout at 1.5x) → crashed → cooldown → next round.
//
// Run:  go run ./tests/crash
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

const (
	rounds       = 10_000
	players      = 5
	stake        = 1_000_000 // 1 USDC per bet
	cashoutBP    = 15000     // cashout at 1.5x
)

type playerState struct {
	betID       uint64
	cashoutSent bool
}

func main() {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, err := os.ReadFile("wasm/crash.wasm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read crash.wasm: %v\n", err)
		os.Exit(1)
	}
	chain.RegisterGame(1, wasmBytes, "crash", 200)
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
		totalCashoutGas  uint64
		totalBlockUpdGas uint64
		placeBetCalls    int
		cashoutCalls     int
		blocks           int
		roundsDone       int
		settledWin       int
		settledLoss      int
	)

	pState := make([]playerState, players)
	phase := ""
	prevPhase := ""
	var multBP uint64
	betsJoined := false

	tStart := time.Now()

	for roundsDone < rounds {
		// Check if calculator is still alive.
		calc, _ := chain.GetCalculator(1)
		if calc.Status != chainsim.CalcStatusActive {
			fmt.Printf("\n  *** CALCULATOR KILLED after %d rounds, %d blocks ***\n", roundsDone, blocks)
			fmt.Printf("  Gas balance: %d\n", chain.GasBalance(1))
			break
		}

		gBefore := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		gAfter := chain.WasmGasUsed(1)
		blocks++
		if gAfter >= gBefore {
			totalBlockUpdGas += gAfter - gBefore
		}

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

		for _, s := range result.Settlements {
			totalPayout += s.Payout
			if s.Kind == 1 {
				settledWin++
			} else {
				settledLoss++
			}
			for i := range pState {
				if pState[i].betID == s.BetID {
					pState[i].betID = 0
					pState[i].cashoutSent = false
				}
			}
		}

		if phase == "open" && prevPhase == "crashed" && betsJoined {
			roundsDone++
			betsJoined = false

			if roundsDone%1_000 == 0 {
				chain.PurgeSettledBets()
			}

			if roundsDone%1_000 == 0 {
				elapsed := time.Since(tStart)
				profit := int64(totalStaked) - int64(totalPayout)
				edge := float64(profit) / float64(totalStaked) * 100
				fmt.Printf("  round %5d/%d  bets=%-8d  edge=%5.2f%%  blocks=%-8d  elapsed=%s\n",
					roundsDone, rounds, placeBetCalls, edge, blocks, elapsed.Round(time.Millisecond))
			}
		}
		prevPhase = phase

		if phase == "open" && !betsJoined {
			for i, addr := range addrs {
				if pState[i].betID > 0 {
					continue
				}
				bal, _ := chain.Balance(addr)
				if bal < stake {
					chain.Deposit(addr, 1_000_000_000_000)
				}

				gB := chain.WasmGasUsed(1)
				betID, err := chain.PlaceBet(addr, brID, 1, stake, nil)
				gA := chain.WasmGasUsed(1)
				if err != nil {
					continue
				}
				pState[i].betID = betID
				pState[i].cashoutSent = false
				placeBetCalls++
				totalStaked += stake
				if gA >= gB {
					totalPlaceBetGas += gA - gB
				}
			}
			betsJoined = true
		}

		if phase == "tick" && multBP >= cashoutBP {
			for i, addr := range addrs {
				if pState[i].betID == 0 || pState[i].cashoutSent {
					continue
				}
				gB := chain.WasmGasUsed(1)
				err := chain.BetAction(addr, pState[i].betID, []byte{1})
				gA := chain.WasmGasUsed(1)
				if err != nil {
					continue
				}
				pState[i].cashoutSent = true
				cashoutCalls++
				if gA >= gB {
					totalCashoutGas += gA - gB
				}
			}
		}
	}

	elapsed := time.Since(tStart)

	totalGas := totalPlaceBetGas + totalCashoutGas + totalBlockUpdGas
	profit := int64(totalStaked) - int64(totalPayout)
	edge := float64(0)
	if totalStaked > 0 {
		edge = float64(profit) / float64(totalStaked) * 100
	}
	kvUsage := chain.KVUsage(1)
	wasmMem := chain.WasmMemorySize(1)
	recycled := chainsim.WasmRecycleCount()
	gasBalance := chain.GasBalance(1)
	avgBlocks := float64(0)
	if roundsDone > 0 {
		avgBlocks = float64(blocks) / float64(roundsDone)
	}

	fmt.Printf(`
CRASH — %d players × %d rounds (cashout at %.1fx)
═══════════════════════════════════════

Bets placed:    %d
Settled:        win=%d  loss=%d
Total staked:   %d uUSDC
Total payout:   %d uUSDC
House profit:   %d uUSDC
House edge:     %.2f%%

GAS BREAKDOWN
  place_bet:    %12d  (%d calls, avg %d)
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
  Blocks:       %d  (avg %.1f per round)
  Elapsed:      %s
`,
		players, roundsDone, float64(cashoutBP)/10000,
		placeBetCalls,
		settledWin, settledLoss,
		totalStaked, totalPayout, profit, edge,
		totalPlaceBetGas, placeBetCalls, safeDiv(totalPlaceBetGas, uint64(placeBetCalls)),
		totalCashoutGas, cashoutCalls, safeDiv(totalCashoutGas, uint64(cashoutCalls)),
		totalBlockUpdGas, blocks, safeDiv(totalBlockUpdGas, uint64(blocks)),
		totalGas,
		safeDiv(totalGas, uint64(max(roundsDone, 1))),
		safeDiv(totalGas, uint64(max(placeBetCalls, 1))),
		kvUsage, wasmMem/1024, recycled, gasBalance, blocks, avgBlocks, elapsed.Round(time.Millisecond),
	)
}

func safeDiv(a, b uint64) uint64 {
	if b == 0 {
		return 0
	}
	return a / b
}
