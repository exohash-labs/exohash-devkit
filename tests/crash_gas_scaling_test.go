package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashGasScaling(t *testing.T) {
	wasmBytes, err := os.ReadFile("../wasm/crash.wasm")
	if err != nil {
		t.Fatalf("read crash.wasm: %v", err)
	}

	for _, numPlayers := range []int{1, 10, 50, 100, 200, 500, 1000} {
		t.Run(fmt.Sprintf("%d_players", numPlayers), func(t *testing.T) {
			params := chainsim.DefaultParams()
			chain := chainsim.NewWithParams(params, 42)
			defer chain.Close()

			chain.Deposit("house", 1_000_000_000_000_000)
			brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)
			chain.RegisterGame(1, wasmBytes, "crash", 200)
			chain.AttachGame(brID, 1)
			chain.InitGame(1, brID)

			for i := 0; i < numPlayers; i++ {
				chain.Deposit(fmt.Sprintf("p%d", i), 1_000_000_000_000)
			}

			// Run until we get one full round with all players.
			activeBets := make(map[int]uint64)
			phase := "waiting"
			allJoined := false
			var settleGas uint64
			var settleTime time.Duration
			var tickGasMax uint64
			var tickTimeMax time.Duration

			for blocks := 0; blocks < 5000; blocks++ {
				gasBefore := chain.WasmGasUsed(1)
				tBefore := time.Now()
				result := chain.AdvanceBlock()
				tAfter := time.Now()
				gasAfter := chain.WasmGasUsed(1)

				blockGas := gasAfter - gasBefore
				blockTime := tAfter.Sub(tBefore)

				newPhase := phase
				for _, ev := range result.CalcEvents {
					if ev.Topic == "state" {
						var state struct{ Phase string `json:"phase"` }
						json.Unmarshal([]byte(ev.Data), &state)
						newPhase = state.Phase
					}
				}

				if len(result.Settlements) > 0 && allJoined {
					settleGas = blockGas
					settleTime = blockTime
				}

				if newPhase == "tick" && allJoined {
					if blockGas > tickGasMax {
						tickGasMax = blockGas
						tickTimeMax = blockTime
					}
				}

				phase = newPhase

				if newPhase == "crashed" {
					if allJoined {
						// Done — we got our measurements.
						break
					}
					activeBets = make(map[int]uint64)
					phase = "waiting"
					continue
				}

				// Place all bets during open.
				if newPhase == "open" && len(activeBets) == 0 {
					for i := 0; i < numPlayers; i++ {
						betID, err := chain.PlaceBet(fmt.Sprintf("p%d", i), brID, 1, 1_000_000, nil)
						if err == nil {
							activeBets[i] = betID
						}
					}
					if len(activeBets) == numPlayers {
						allJoined = true
					}
				}

				// Handle settlements.
				for _, s := range result.Settlements {
					for idx, betID := range activeBets {
						if betID == s.BetID {
							delete(activeBets, idx)
						}
					}
				}
			}

			nsPerGas := float64(0)
			if settleGas > 0 {
				nsPerGas = float64(settleTime.Nanoseconds()) / float64(settleGas)
			}

			fmt.Fprintf(os.Stderr, "  %4d players: tick_max=%10d gas (%6.2fms)  settle=%10d gas (%6.2fms)  ns/gas=%.2f\n",
				numPlayers,
				tickGasMax, float64(tickTimeMax.Microseconds())/1000,
				settleGas, float64(settleTime.Microseconds())/1000,
				nsPerGas)
		})
	}

	// Extrapolation.
	fmt.Fprintf(os.Stderr, "\n  Extrapolation at 200M gas limit:\n")
	fmt.Fprintf(os.Stderr, "  If 1 gas ≈ X ns, then 200M gas ≈ 200M×X ms\n")
}
