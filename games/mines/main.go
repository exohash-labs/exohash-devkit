// Mines unified test suite.
//
// Three sections: house edge convergence, wrong-player behavior, gas.
// Emits stdout report, README.md (linked from scanner verification panel),
// and results.txt (CI summary line).
//
// Run from this directory:  go run .
package main

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

// --- Configuration ---

const (
	wasmPath        = "mines.wasm"
	mdPath          = "README.md"
	resultsPath     = "results.txt"
	houseEdgeBp     = 100 // 1%
	edgeMarginFloor = 20  // minimum tolerance (bp) when 4σ is smaller
	edgeRounds      = 50_000
	stakeUusdc      = 1_000_000
	bigHouseFunds   = 10_000_000_000_000_000
	boardSize       = 25
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
	table   string
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

// placeBet params: mines count (1 byte). Chainsim prepends 20-byte sender.
func placeBetParams(mines byte) []byte {
	return []byte{mines}
}

// bet_action payloads.
func revealAction(tile byte) []byte {
	return []byte{1, tile}
}

func cashoutAction() []byte {
	return []byte{2}
}

func mustReadWasm() []byte {
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", wasmPath, err)
		os.Exit(2)
	}
	return wasm
}

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
	if err := chain.RegisterGame(1, mustReadWasm(), "mines", houseEdgeBp); err != nil {
		panic(fmt.Sprintf("register mines: %v", err))
	}
	if err := chain.AttachGame(brID, 1); err != nil {
		panic(fmt.Sprintf("attach: %v", err))
	}
	if err := chain.InitGame(1, brID); err != nil {
		panic(fmt.Sprintf("init: %v", err))
	}
	return chain
}

// Binomial helpers for expected edge.
// C(n,k) — small k so this stays exact.
func binom(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	if k > n-k {
		k = n - k
	}
	r := 1.0
	for i := 0; i < k; i++ {
		r *= float64(n - i)
		r /= float64(i + 1)
	}
	return r
}

// pSafe: probability all `reveals` reveals land on safe tiles given `mines` mines.
// C(boardSize-mines, reveals) / C(boardSize, reveals)
func pSafe(mines, reveals int) float64 {
	if reveals < 1 {
		return 1.0
	}
	return binom(boardSize-mines, reveals) / binom(boardSize, reveals)
}

// fairMult: fair payout multiplier for revealing `reveals` safe tiles with `mines` mines.
// Inverse of pSafe.
func fairMult(mines, reveals int) float64 {
	p := pSafe(mines, reveals)
	if p == 0 {
		return 0
	}
	return 1.0 / p
}

// edgedMult: multiplier after applying house edge.
func edgedMult(mines, reveals int) float64 {
	return fairMult(mines, reveals) * float64(10000-houseEdgeBp) / 10000
}

// Run one bet at (mines, reveals) — player reveals `reveals` tiles (0,1,2,...) then cashes out.
// Returns payout received (0 if mine hit or refund).
func playRound(c *chainsim.Chain, player string, mines, reveals int) (payout uint64, err error) {
	betID, err := c.PlaceBet(player, 1, 1, stakeUusdc, placeBetParams(byte(mines)))
	if err != nil {
		return 0, err
	}

	for i := 0; i < reveals; i++ {
		if err := c.BetAction(player, betID, revealAction(byte(i))); err != nil {
			return 0, err
		}
		r := c.AdvanceBlock()
		// Check if this reveal caused a settlement (mine hit).
		for _, s := range r.Settlements {
			if s.BetID == betID {
				return s.Payout, nil
			}
		}
	}

	// Cashout.
	if err := c.BetAction(player, betID, cashoutAction()); err != nil {
		return 0, err
	}
	// Cashout settles immediately; advance once to drain events.
	r := c.AdvanceBlock()
	for _, s := range r.Settlements {
		if s.BetID == betID {
			return s.Payout, nil
		}
	}
	// Cashout payout may have been delivered via the pre-drain settlements buffer.
	b := c.GetBet(betID)
	if b != nil {
		return b.Payout, nil
	}
	return 0, nil
}

// --- Section 1: house edge convergence ---

func runEdgeSection() section {
	// (mines, reveals) combos chosen to exercise different parts of the
	// multiplier table while keeping variance manageable at edgeRounds samples.
	type cfg struct {
		mines   int
		reveals int
	}
	configs := []cfg{
		{1, 1}, {1, 3}, {1, 5},
		{3, 1}, {3, 3}, {3, 5},
		{5, 2}, {5, 4},
		{8, 1}, {8, 3},
	}

	var results []result
	var tbl strings.Builder
	tbl.WriteString("| Mines | Reveals | P(all safe) | Multiplier | Bets   | Realized edge | Tol (4σ) | Delta | Verdict |\n")
	tbl.WriteString("|-------|---------|-------------|------------|--------|---------------|----------|-------|---------|\n")

	for _, k := range configs {
		p := pSafe(k.mines, k.reveals)
		mult := edgedMult(k.mines, k.reveals)

		chain := newChain(nil, bigHouseFunds)
		chain.Deposit("p1", 1_000_000_000_000)

		var staked, paid uint64
		failed := false
		for i := 0; i < edgeRounds; i++ {
			payout, err := playRound(chain, "p1", k.mines, k.reveals)
			if err != nil {
				results = append(results, result{
					name:   fmt.Sprintf("mines=%d reveals=%d", k.mines, k.reveals),
					pass:   false,
					detail: fmt.Sprintf("round %d: %v", i, err),
				})
				failed = true
				break
			}
			staked += stakeUusdc
			paid += payout
			if i%1_000 == 0 {
				chain.PurgeSettledBets()
			}
		}
		chain.Close()
		if failed {
			continue
		}

		// Realized edge, int64 to avoid uint underflow on favourable runs.
		edgeBp := float64(int64(staked)-int64(paid)) / float64(staked) * 10000

		// Single-bet variance:
		//   X = mult w.p. p, 0 w.p. 1-p.
		//   E[X]  = p·mult      = (1 - edge)
		//   Var   = p·mult²·(1-p)
		//   σ(edge) = √(Var/N) * 10000
		varBet := p * mult * mult * (1 - p)
		sigmaBp := math.Sqrt(varBet/float64(edgeRounds)) * 10000
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
		tbl.WriteString(fmt.Sprintf("| %5d | %7d | %10.4f  | %9.3fx | %6d | %9.1f bp  | %6.1f bp | %5.1f | %s |\n",
			k.mines, k.reveals, p, mult, edgeRounds, edgeBp, tolBp, deltaBp, mark))
		results = append(results, result{
			name:   fmt.Sprintf("mines=%d reveals=%d", k.mines, k.reveals),
			pass:   pass,
			detail: fmt.Sprintf("realized=%.1fbp tol(4σ)=%.1fbp delta=%.1fbp staked=%d paid=%d", edgeBp, tolBp, deltaBp, staked, paid),
		})
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

	// place_bet validation

	runCase("params too short (<21 bytes)", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		return err
	}, "status=11")

	for _, m := range []byte{0, 14, 255} {
		m := m
		runCase(fmt.Sprintf("mines=%d out of [1,13]", m), func() error {
			c := newChain(nil, bigHouseFunds)
			defer c.Close()
			c.Deposit("p1", 1_000_000_000)
			_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(m))
			return err
		}, "status=12")
	}

	runCase("stake=0", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, 0, placeBetParams(3))
		return err
	}, "stake must be > 0")

	runCase("stake below MinStakeUusdc", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, 1000, placeBetParams(3))
		return err
	}, "below minimum")

	runCase("stake > player balance", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("poor", 50_000)
		_, err := c.PlaceBet("poor", 1, 1, 1_000_000, placeBetParams(3))
		return err
	}, "insufficient balance")

	// Reserve cap: tiny bankroll, low mines count → huge max multiplier at maxReveals.
	// With 1 mine and 5 reveals, fair mult = C(25,5)/C(24,5) = 53130/42504 ≈ 1.25x.
	// Not big. To trip cap we need HIGH mines count. With 13 mines, 5 reveals:
	// C(25,5)/C(12,5) = 53130/792 ≈ 67x. Stake 10M → max 670M. If bankroll < 33B → 2% cap < 670M, reject.
	runCase("max_payout exceeds bankroll cap", func() error {
		p := chainsim.DefaultParams()
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", 10_000_000_000) // 10K USDC
		brID, _ := c.CreateBankroll("house", 10_000_000_000, "Tiny", false)
		c.RegisterGame(1, mustReadWasm(), "mines", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000_000)
		_, err := c.PlaceBet("p1", brID, 1, 10_000_000, placeBetParams(13))
		return err
	}, "status=3")

	// bet_action validation

	runCase("bet_action: empty payload", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return c.BetAction("p1", id, []byte{})
	}, "status=1")

	for _, a := range []byte{0, 3, 255} {
		a := a
		runCase(fmt.Sprintf("bet_action: unknown action=%d", a), func() error {
			c := newChain(nil, bigHouseFunds)
			defer c.Close()
			c.Deposit("p1", 1_000_000_000)
			id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
			return c.BetAction("p1", id, []byte{a})
		}, "status=2")
	}

	runCase("reveal: tile index >= 25", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return c.BetAction("p1", id, revealAction(25))
	}, "status=34")

	runCase("reveal: payload missing tile byte", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return c.BetAction("p1", id, []byte{1})
	}, "status=33")

	runCase("reveal: same tile twice", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		if err := c.BetAction("p1", id, revealAction(0)); err != nil {
			return err
		}
		c.AdvanceBlock() // resolve reveal
		return c.BetAction("p1", id, revealAction(0))
	}, "status=35")

	runCase("reveal: during waiting-RNG phase", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		if err := c.BetAction("p1", id, revealAction(0)); err != nil {
			return err
		}
		// Don't advance — bet is still in waiting-RNG phase.
		return c.BetAction("p1", id, revealAction(1))
	}, "status=31")

	runCase("cashout: before any reveal", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return c.BetAction("p1", id, cashoutAction())
	}, "status=43")

	runCase("cashout: during waiting-RNG phase", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		if err := c.BetAction("p1", id, revealAction(0)); err != nil {
			return err
		}
		// Still waiting for seed resolution.
		return c.BetAction("p1", id, cashoutAction())
	}, "status=41")

	// Cross-cutting

	runCase("bet on unattached game", func() error {
		c := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "mines", houseEdgeBp)
		// Skip AttachGame.
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", brID, 1, stakeUusdc, placeBetParams(3))
		return err
	}, "not active on bankroll")

	runCase("bet on killed calculator", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		_ = c.KillCalculator(1, "test")
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return err
	}, "not active")

	runCase("bet while beacon down", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		c.SetBeaconAvailable(false)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, placeBetParams(3))
		return err
	}, "beacon randomness unavailable")

	return section{title: "Wrong-player behavior (rejection semantics)", results: results}
}

// --- Section 3: gas ---

func runGasSection() section {
	var results []result

	{
		c := newChain(nil, bigHouseFunds)
		want := c.Params().GasInitialCredits
		got := c.GasBalance(1)
		pass := got == want
		detail := fmt.Sprintf("want=%d got=%d", want, got)
		c.Close()
		results = append(results, result{name: "initial gas balance = params.GasInitialCredits", pass: pass, detail: detail})
	}

	// Per-bet gas is O(1) in history. A "bet" here is place_bet + 2 reveals + cashout.
	{
		c := newChain(nil, bigHouseFunds)
		c.Deposit("p1", 1_000_000_000_000)
		sample := func(n int) uint64 {
			var total uint64
			for i := 0; i < n; i++ {
				before := c.WasmGasUsed(1)
				if _, err := playRound(c, "p1", 3, 2); err != nil {
					return 0
				}
				after := c.WasmGasUsed(1)
				if after >= before {
					total += after - before
				}
			}
			if n == 0 {
				return 0
			}
			return total / uint64(n)
		}
		at100 := sample(100)
		at500 := sample(500)
		at2000 := sample(2000)
		c.Close()
		if at100 == 0 {
			results = append(results, result{name: "per-round gas is O(1) in history", pass: false, detail: "no gas observed at n=100"})
		} else {
			grew := float64(at2000) / float64(at100)
			pass := grew < 1.30
			detail := fmt.Sprintf("avg WASM gas/round: 100→%d, 500→%d, 2000→%d (2000/100 = %.2fx)", at100, at500, at2000, grew)
			results = append(results, result{name: "per-round gas is O(1) in history", pass: pass, detail: detail})
		}
	}

	// Balance invariant.
	{
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		initial := c.GasBalance(1)
		credit := c.Params().GasCreditPerBet
		c.Deposit("p1", 1_000_000_000_000)
		const n = 100
		for i := 0; i < n; i++ {
			_, _ = playRound(c, "p1", 3, 2)
		}
		final := c.GasBalance(1)
		ceiling := initial + uint64(n)*credit
		pass := final <= ceiling && final > 0
		detail := fmt.Sprintf("initial=%d final=%d ceiling(initial+n·credit)=%d", initial, final, ceiling)
		results = append(results, result{name: "gas balance stays under (initial + N·credit)", pass: pass, detail: detail})
	}

	// Gas exhaustion kills the calculator. Mines is multi-step
	// (place_bet + reveal + block_update resolution + cashout), so
	// exhaustion may surface as either "gas balance exhausted" (direct)
	// or as a downstream WASM status (e.g. a cashout rejected because
	// the bet got stuck in waiting-RNG when block_update died). Either
	// way, calc.Status = Killed is the authoritative check.
	{
		p := chainsim.DefaultParams()
		p.GasInitialCredits = 400_000
		p.GasCreditPerBet = 0
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "mines", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000)
		var killedAt int
		for i := 0; i < 200; i++ {
			_, _ = playRound(c, "p1", 3, 2)
			calc, _ := c.GetCalculator(1)
			if calc != nil && calc.Status == chainsim.CalcStatusKilled {
				killedAt = i + 1
				break
			}
		}
		calc, _ := c.GetCalculator(1)
		pass := calc != nil && calc.Status == chainsim.CalcStatusKilled
		detail := fmt.Sprintf("killedAt=%d status=%d (kill reason emitted as event)", killedAt, statusCode(calc))
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
	fmt.Fprintf(&b, "\n=== Mines unified test suite ===\n\n")
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
	fmt.Fprintf(&b, "# Mines\n\n")
	fmt.Fprintf(&b, "5×5 minefield — reveal tiles, avoid mines, cashout anytime.\n\n")
	fmt.Fprintf(&b, "**House edge: %.2f%%** (%d bp). Each reveal uses a fresh beacon seed — mines are not pre-placed.\n\n", float64(houseEdgeBp)/100, houseEdgeBp)
	fmt.Fprintf(&b, "**Source:** [`src/main.go`](./src/main.go) · **Binary:** [`mines.wasm`](./mines.wasm) · **Tests:** [`main.go`](./main.go)\n\n")
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
	return fmt.Sprintf("mines tests: passed=%d total=%d\n", passed, total)
}

// --- Entry point ---

func main() {
	if _, err := os.Stat(wasmPath); err != nil {
		fmt.Fprintf(os.Stderr, "missing %s — build WASM first (tinygo build -target=wasi -no-debug -opt=2 ./src)\n", wasmPath)
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
