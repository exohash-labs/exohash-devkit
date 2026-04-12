package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func TestCrashGCTrace10K(t *testing.T) {
	chain := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
	defer chain.Close()

	chain.Deposit("house", 1_000_000_000_000_000)
	brID, _ := chain.CreateBankroll("house", 1_000_000_000_000_000, "Test", false)

	wasmBytes, _ := os.ReadFile("../wasm/crash.wasm")
	chain.RegisterGame(1, wasmBytes, "crash", 200)
	chain.AttachGame(brID, 1)
	chain.InitGame(1, brID)

	const numPlayers = 100
	for i := 0; i < numPlayers; i++ {
		chain.Deposit(fmt.Sprintf("p%d", i), 1_000_000_000_000)
	}

	activeBets := make(map[int]uint64)
	phase := "waiting"

	const totalBlocks = 100000
	var gcCount int
	var gcTotal uint64
	var settleCount int
	var settleTotal uint64
	var maxGC, maxSettle uint64

	fmt.Fprintf(os.Stderr, "\n=== Spikes over %dK blocks, %d players (>100K gas) ===\n\n", totalBlocks/1000, numPlayers)

	for b := 0; b < totalBlocks; b++ {
		gasBefore := chain.WasmGasUsed(1)
		result := chain.AdvanceBlock()
		gasAfter := chain.WasmGasUsed(1)

		if gasAfter < gasBefore {
			continue
		}
		gas := gasAfter - gasBefore
		if gas > 100000 {
			settled := len(result.Settlements)
			if settled > 0 {
				settleCount++
				settleTotal += gas
				if gas > maxSettle { maxSettle = gas }
			} else {
				gcCount++
				gcTotal += gas
				if gas > maxGC { maxGC = gas }
			}
		}

		// Detect phase from events.
		newPhase := phase
		for _, ev := range result.CalcEvents {
			if ev.Topic == "state" {
				var s struct{ Phase string `json:"phase"` }
				json.Unmarshal([]byte(ev.Data), &s)
				newPhase = s.Phase
			}
		}

		for range result.Settlements {
			activeBets = make(map[int]uint64)
		}

		if newPhase == "crashed" {
			activeBets = make(map[int]uint64)
			phase = "waiting"
			continue
		}
		phase = newPhase

		// Place bets during open.
		if newPhase == "open" && len(activeBets) == 0 {
			for i := 0; i < numPlayers; i++ {
				addr := fmt.Sprintf("p%d", i)
				bal, _ := chain.Balance(addr)
				if bal < 1_000_000 {
					chain.Deposit(addr, 1_000_000_000_000)
				}
				betID, err := chain.PlaceBet(addr, brID, 1, 1_000_000, nil)
				if err == nil {
					activeBets[i] = betID
				}
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n  GC spikes:    n=%d  avg=%d  max=%d\n", gcCount, safeDiv(gcTotal, uint64(gcCount)), maxGC)
	fmt.Fprintf(os.Stderr, "  Settle spikes: n=%d  avg=%d  max=%d\n\n", settleCount, safeDiv(settleTotal, uint64(settleCount)), maxSettle)
}

func safeDiv(a, b uint64) uint64 {
	if b == 0 { return 0 }
	return a / b
}
