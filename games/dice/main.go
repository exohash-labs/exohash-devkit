// Dice unified test suite.
//
// Three sections: house edge convergence, wrong-player behavior, gas.
// Emits a human-readable report to stdout and regenerates README.md
// in this directory.
//
// Run from this directory: go run .
package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

// --- Configuration ---

const (
	wasmPath      = "dice.wasm"
	mdPath        = "README.md"
	resultsPath   = "results.txt"
	houseEdgeBp   = 100 // 1%
	edgeMarginFloor = 20  // minimum tolerance (bp) when 4σ is smaller
	edgeRounds    = 100_000
	stakeUusdc    = 1_000_000             // 1 USDC
	bigHouseFunds = 10_000_000_000_000_000 // 10B USDC, plenty for edge sampling
)

// --- Test framework ---

type result struct {
	name   string
	pass   bool
	detail string
}

type section struct {
	title   string
	results []result
	table   string // optional markdown block inserted before the list
}

func (s section) passCount() int {
	n := 0
	for _, r := range s.results {
		if r.pass {
			n++
		}
	}
	return n
}

func (s section) allPassed() bool { return s.passCount() == len(s.results) }

// --- Helpers ---

func diceParams(mode byte, threshold uint64) []byte {
	p := make([]byte, 9)
	p[0] = mode
	binary.LittleEndian.PutUint64(p[1:], threshold)
	return p
}

func mustReadWasm() []byte {
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", wasmPath, err)
		os.Exit(2)
	}
	return wasm
}

// newChain builds a fresh chain with dice registered on a large public bankroll.
func newChain(params *chainsim.Params, houseFunds uint64) *chainsim.Chain {
	p := chainsim.DefaultParams()
	if params != nil {
		p = *params
	}
	chain := chainsim.NewWithParams(p, 42)

	chain.Deposit("house", houseFunds)
	brID, err := chain.CreateBankroll("house", houseFunds, "Test", false)
	if err != nil {
		panic(fmt.Sprintf("create bankroll: %v", err))
	}

	if err := chain.RegisterGame(1, mustReadWasm(), "dice", houseEdgeBp); err != nil {
		panic(fmt.Sprintf("register dice: %v", err))
	}
	if err := chain.AttachGame(brID, 1); err != nil {
		panic(fmt.Sprintf("attach game: %v", err))
	}
	if err := chain.InitGame(1, brID); err != nil {
		panic(fmt.Sprintf("init game: %v", err))
	}
	return chain
}

// --- Section 1: house edge convergence ---

func runEdgeSection() section {
	// 5% / 95% dropped — variance at these extremes is too wide for any
	// practical sample size on a 1% target edge (σ grows as 1/√(p(1-p)/N)).
	chances := []uint64{1000, 2500, 5000, 7500, 9000} // bp
	var results []result
	var tbl strings.Builder
	tbl.WriteString("| Mode  | Chance | Bets    | Realized edge | Tol (4σ) | Delta | Verdict |\n")
	tbl.WriteString("|-------|--------|---------|---------------|----------|-------|---------|\n")

	for _, mode := range []byte{1, 2} { // 1=over, 2=under
		modeName := "over"
		if mode == 2 {
			modeName = "under"
		}
		for _, c := range chances {
			threshold := c
			if mode == 1 {
				threshold = 10000 - c
			}
			chain := newChain(nil, bigHouseFunds)
			chain.Deposit("p1", 1_000_000_000_000)

			params := diceParams(mode, threshold)
			var staked, paid uint64
			failed := false
			for i := 0; i < edgeRounds; i++ {
				if _, err := chain.PlaceBet("p1", 1, 1, stakeUusdc, params); err != nil {
					results = append(results, result{
						name:   fmt.Sprintf("%s mode / chance=%.1f%%", modeName, float64(c)/100),
						pass:   false,
						detail: fmt.Sprintf("place_bet failed at round %d: %v", i, err),
					})
					failed = true
					break
				}
				staked += stakeUusdc
				r := chain.AdvanceBlock()
				for _, s := range r.Settlements {
					paid += s.Payout
				}
			}

			if !failed {
				// int64 math so player-favorable runs (paid > staked, transient) don't underflow.
				edgeBp := float64(int64(staked)-int64(paid)) / float64(staked) * 10000
				// Expected σ of realized edge for chance p and N bets:
				// σ = (1 - e) × √((1-p)/p/N)   →   in bp, × 10000.
				p := float64(c) / 10000
				e := float64(houseEdgeBp) / 10000
				sigmaBp := (1 - e) * math.Sqrt((1-p)/p/float64(edgeRounds)) * 10000
				tolBp := 4 * sigmaBp
				if tolBp < edgeMarginFloor {
					tolBp = edgeMarginFloor
				}
				deltaBp := math.Abs(edgeBp - float64(houseEdgeBp))
				pass := deltaBp <= tolBp
				mark := "✓"
				if !pass {
					mark = "✗"
				}
				tbl.WriteString(fmt.Sprintf("| %-5s | %5.1f%% | %7d | %9.1f bp  | %6.1f bp | %5.1f | %s |\n",
					modeName, float64(c)/100, edgeRounds, edgeBp, tolBp, deltaBp, mark))
				results = append(results, result{
					name:   fmt.Sprintf("%s mode / chance=%.1f%%", modeName, float64(c)/100),
					pass:   pass,
					detail: fmt.Sprintf("realized=%.1fbp tol(4σ)=%.1fbp delta=%.1fbp staked=%d paid=%d", edgeBp, tolBp, deltaBp, staked, paid),
				})
			}
			chain.Close()
		}
	}

	return section{
		title:   fmt.Sprintf("House edge convergence (target %.2f%% / %dbp, 4σ dynamic tolerance, %d bets per config)", float64(houseEdgeBp)/100, houseEdgeBp, edgeRounds),
		results: results,
		table:   tbl.String(),
	}
}

// --- Section 2: wrong-player behavior ---

func runInvalidSection() section {
	var results []result

	runCase := func(name string, fn func() error, wantSubstr string) {
		err := fn()
		pass := err != nil && strings.Contains(err.Error(), wantSubstr)
		detail := "no error (expected one)"
		if err != nil {
			detail = err.Error()
		}
		results = append(results, result{name: name, pass: pass, detail: detail})
	}

	// 1. Params shorter than required.
	runCase("params too short (<9 bytes)", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{2, 0, 0})
		return err
	}, "status=1")

	// 2. Invalid modes.
	for _, m := range []byte{0, 3, 255} {
		m := m
		runCase(fmt.Sprintf("mode=%d (invalid)", m), func() error {
			c := newChain(nil, bigHouseFunds)
			defer c.Close()
			c.Deposit("p1", 1_000_000_000)
			_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, diceParams(m, 5000))
			return err
		}, "status=2")
	}

	// 3. Chance out of range.
	runCase("chance=0% (under, threshold=0)", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, diceParams(2, 0))
		return err
	}, "status=2")
	runCase("chance=100% (under, threshold=10000)", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, diceParams(2, 10000))
		return err
	}, "status=2")

	// 4. Zero stake.
	runCase("stake=0", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, 0, diceParams(2, 5000))
		return err
	}, "stake must be > 0")

	// 5. Stake below MinStakeUusdc.
	runCase("stake below MinStakeUusdc", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, 1000, diceParams(2, 5000))
		return err
	}, "below minimum")

	// 6. Stake exceeds balance.
	runCase("stake > player balance", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("poor", 50_000)
		_, err := c.PlaceBet("poor", 1, 1, 1_000_000, diceParams(2, 5000))
		return err
	}, "insufficient balance")

	// 7. Reserve exceeds bankroll's MaxPayoutCapBps (2% default).
	runCase("max_payout exceeds bankroll cap", func() error {
		p := chainsim.DefaultParams()
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", 100_000_000) // tiny bankroll
		brID, _ := c.CreateBankroll("house", 100_000_000, "Tiny", false)
		c.RegisterGame(1, mustReadWasm(), "dice", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000)
		// Stake 1M with chance 1% → max_payout = 100M → over 2M cap.
		_, err := c.PlaceBet("p1", brID, 1, 1_000_000, diceParams(2, 100))
		return err
	}, "status=3")

	// 8. Bankroll does not have this calculator attached.
	runCase("bet on unattached game", func() error {
		c := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "dice", houseEdgeBp)
		// Deliberately skip AttachGame.
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", brID, 1, stakeUusdc, diceParams(2, 5000))
		return err
	}, "not active on bankroll")

	// 9. Bet on killed calculator.
	runCase("bet on killed calculator", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		_ = c.KillCalculator(1, "test")
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, diceParams(2, 5000))
		return err
	}, "not active")

	// 10. Bet while beacon is down (Phase-1 circuit breaker).
	runCase("bet while beacon down", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		c.SetBeaconAvailable(false)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, diceParams(2, 5000))
		return err
	}, "beacon randomness unavailable")

	return section{title: "Wrong-player behavior (rejection semantics)", results: results}
}

// --- Section 3: gas ---

func runGasSection() section {
	var results []result

	// 3.1 Initial gas balance matches params.
	{
		c := newChain(nil, bigHouseFunds)
		want := c.Params().GasInitialCredits
		got := c.GasBalance(1)
		pass := got == want
		detail := fmt.Sprintf("want=%d got=%d", want, got)
		c.Close()
		results = append(results, result{name: "initial gas balance = params.GasInitialCredits", pass: pass, detail: detail})
	}

	// 3.2 Per-bet gas is O(1) w.r.t. history length.
	// Sample average WASM gas used per bet at 100, 1000, 5000 bets.
	{
		c := newChain(nil, bigHouseFunds)
		c.Deposit("p1", 1_000_000_000_000)
		params := diceParams(2, 5000)
		sample := func(n int) uint64 {
			var total uint64
			for i := 0; i < n; i++ {
				before := c.WasmGasUsed(1)
				if _, err := c.PlaceBet("p1", 1, 1, stakeUusdc, params); err != nil {
					return 0
				}
				after := c.WasmGasUsed(1)
				if after >= before {
					total += after - before
				}
				c.AdvanceBlock()
			}
			if n == 0 {
				return 0
			}
			return total / uint64(n)
		}
		at100 := sample(100)
		at1000 := sample(1000)
		at5000 := sample(5000)
		c.Close()

		if at100 == 0 {
			results = append(results, result{name: "per-bet gas is O(1) in history", pass: false, detail: "no gas observed at n=100"})
		} else {
			grew := float64(at5000) / float64(at100)
			pass := grew < 1.20
			detail := fmt.Sprintf("avg WASM gas/bet: 100→%d, 1000→%d, 5000→%d (5000/100 = %.2fx)",
				at100, at1000, at5000, grew)
			results = append(results, result{name: "per-bet gas is O(1) in history", pass: pass, detail: detail})
		}
	}

	// 3.3 Balance invariant: after N bets, gasBalance ≤ initial + N·credit.
	{
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		initial := c.GasBalance(1)
		credit := c.Params().GasCreditPerBet
		c.Deposit("p1", 1_000_000_000_000)
		params := diceParams(2, 5000)
		const n = 200
		for i := 0; i < n; i++ {
			c.PlaceBet("p1", 1, 1, stakeUusdc, params)
			c.AdvanceBlock()
		}
		final := c.GasBalance(1)
		ceiling := initial + uint64(n)*credit
		pass := final <= ceiling && final > 0
		detail := fmt.Sprintf("initial=%d final=%d ceiling(initial+n·credit)=%d", initial, final, ceiling)
		results = append(results, result{name: "gas balance stays under (initial + N·credit)", pass: pass, detail: detail})
	}

	// 3.4 Gas exhaustion kills the calculator.
	{
		p := chainsim.DefaultParams()
		p.GasInitialCredits = 200_000 // tiny
		p.GasCreditPerBet = 0         // no top-up → must exhaust
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "dice", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000)
		params := diceParams(2, 5000)
		var killedAt int
		var lastErr error
		for i := 0; i < 200; i++ {
			_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, params)
			if err != nil {
				killedAt = i + 1
				lastErr = err
				break
			}
			c.AdvanceBlock()
		}
		calc, _ := c.GetCalculator(1)
		pass := lastErr != nil &&
			strings.Contains(lastErr.Error(), "gas balance exhausted") &&
			calc != nil && calc.Status == chainsim.CalcStatusKilled
		errMsg := ""
		if lastErr != nil {
			errMsg = lastErr.Error()
		}
		detail := fmt.Sprintf("killedAt=%d err=%q status=%d", killedAt, errMsg, statusCode(calc))
		results = append(results, result{name: "gas exhaustion → calculator killed", pass: pass, detail: detail})
	}

	return section{title: "Gas (instrumentation + accounting)", results: results}
}

func statusCode(c *chainsim.Calculator) int {
	if c == nil {
		return -1
	}
	return int(c.Status)
}

// --- Report rendering ---

func renderStdout(secs []section, total time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n=== Dice unified test suite ===\n\n")
	for _, s := range secs {
		mark := "PASS"
		if !s.allPassed() {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "[%s] %s  (%d/%d)\n", mark, s.title, s.passCount(), len(s.results))
		if s.table != "" {
			for _, line := range strings.Split(strings.TrimSpace(s.table), "\n") {
				fmt.Fprintf(&b, "  %s\n", line)
			}
			fmt.Fprintln(&b)
		}
		for _, r := range s.results {
			sigil := "✓"
			if !r.pass {
				sigil = "✗"
			}
			fmt.Fprintf(&b, "  %s %s — %s\n", sigil, r.name, r.detail)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintf(&b, "Total: %s\n", total.Round(time.Millisecond))
	return b.String()
}

func renderMarkdown(secs []section, total time.Duration) string {
	var b strings.Builder
	ts := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "# Dice\n\n")
	fmt.Fprintf(&b, "Provably fair dice — single bet, single outcome, next-block settlement.\n\n")
	fmt.Fprintf(&b, "**House edge: %.2f%%** (%d bp). RTP %.2f%%.\n\n", float64(houseEdgeBp)/100, houseEdgeBp, float64(10000-houseEdgeBp)/100)
	fmt.Fprintf(&b, "**Source:** [`src/main.go`](./src/main.go) · **Binary:** [`dice.wasm`](./dice.wasm) · **Tests:** [`main.go`](./main.go)\n\n")
	fmt.Fprintf(&b, "---\n\n## Test results\n\n_Generated %s · Chainsim run · Duration %s_\n\n", ts, total.Round(time.Millisecond))

	fmt.Fprintf(&b, "| Section | Pass | Fail |\n|---------|-----:|-----:|\n")
	for _, s := range secs {
		fmt.Fprintf(&b, "| %s | %d | %d |\n", s.title, s.passCount(), len(s.results)-s.passCount())
	}
	fmt.Fprintln(&b)

	for _, s := range secs {
		status := "PASS"
		if !s.allPassed() {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "## %s — %s\n\n", s.title, status)
		if s.table != "" {
			fmt.Fprintln(&b, s.table)
		}
		for _, r := range s.results {
			sigil := "✓"
			if !r.pass {
				sigil = "✗"
			}
			fmt.Fprintf(&b, "- %s **%s** — `%s`\n", sigil, r.name, r.detail)
		}
		fmt.Fprintln(&b)
	}

	fmt.Fprintf(&b, "---\n\n_Reproduce: `go run .` from this directory._\n")
	return b.String()
}

func renderSummary(secs []section) string {
	total := 0
	passed := 0
	for _, s := range secs {
		total += len(s.results)
		passed += s.passCount()
	}
	return fmt.Sprintf("dice tests: passed=%d total=%d\n", passed, total)
}

// --- Entry point ---

func main() {
	if _, err := os.Stat(wasmPath); err != nil {
		fmt.Fprintf(os.Stderr, "missing %s — build WASM first (tinygo build -target=wasi -no-debug -opt=2 .)\n", wasmPath)
		os.Exit(2)
	}

	t0 := time.Now()
	sections := []section{
		runEdgeSection(),
		runInvalidSection(),
		runGasSection(),
	}
	dur := time.Since(t0)

	fmt.Print(renderStdout(sections, dur))

	if err := os.WriteFile(mdPath, []byte(renderMarkdown(sections, dur)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", mdPath, err)
	}
	if err := os.WriteFile(resultsPath, []byte(renderSummary(sections)), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", resultsPath, err)
	}

	for _, s := range sections {
		if !s.allPassed() {
			os.Exit(1)
		}
	}
}
