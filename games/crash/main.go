// Crash unified test suite.
//
// Three sections: house edge convergence across cashout targets,
// wrong-player behavior, gas.
//
// Crash is round-based (open → tick → crashed → open). The test batches
// many bets into each round to amortize the 16-block open window +
// 5-block cooldown, keeping runtime manageable.
//
// Run from this directory:  go run .
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

// --- Configuration ---

const (
	wasmPath        = "crash.wasm"
	mdPath          = "README.md"
	resultsPath     = "results.txt"
	houseEdgeBp     = 100 // 1%
	edgeMarginFloor = 20
	edgeRoundsPer   = 200  // rounds per cashout target (each round = many bets)
	betsPerRound    = 50   // players piling into each round at same target
	stakeUusdc      = 1_000_000
	bigHouseFunds   = 10_000_000_000_000_000
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
	if err := chain.RegisterGame(1, mustReadWasm(), "crash", houseEdgeBp); err != nil {
		panic(fmt.Sprintf("register crash: %v", err))
	}
	if err := chain.AttachGame(brID, 1); err != nil {
		panic(fmt.Sprintf("attach: %v", err))
	}
	if err := chain.InitGame(1, brID); err != nil {
		panic(fmt.Sprintf("init: %v", err))
	}
	return chain
}

// Round state snapshot extracted from WASM query().
type roundState struct {
	Round      uint64 `json:"round"`
	Phase      string `json:"phase"`
	MultBP     uint64 `json:"mult_bp"`
	Tick       uint64 `json:"tick"`
	BlocksLeft uint64 `json:"blocks_left"`
}

func queryRound(c *chainsim.Chain) roundState {
	raw, err := c.GameQuery(1)
	if err != nil || raw == nil {
		return roundState{Phase: "unknown"}
	}
	var s roundState
	_ = json.Unmarshal(raw, &s)
	return s
}

// advanceTo drives blocks forward until query() reports the target phase,
// with a safety cap to avoid infinite loops.
func advanceTo(c *chainsim.Chain, targetPhase string, maxBlocks int) error {
	for i := 0; i < maxBlocks; i++ {
		s := queryRound(c)
		if s.Phase == targetPhase {
			return nil
		}
		c.AdvanceBlock()
	}
	return fmt.Errorf("never reached phase %q within %d blocks (last seen %q)", targetPhase, maxBlocks, queryRound(c).Phase)
}

// playRound places N bets in the OPEN phase, all with the same cashout target
// (in bp of multiplier). Returns total staked + total paid.
func playRound(c *chainsim.Chain, players []string, targetMultBP uint64) (staked, paid uint64, err error) {
	if err := advanceTo(c, "open", 40); err != nil {
		return 0, 0, err
	}

	// Place one bet per player in current OPEN phase. Params are empty —
	// crash takes stake only, no player-chosen target (cashout is a later action).
	betIDs := make([]uint64, 0, len(players))
	for _, addr := range players {
		id, e := c.PlaceBet(addr, 1, 1, stakeUusdc, []byte{})
		if e != nil {
			return staked, paid, fmt.Errorf("place_bet for %s: %w", addr, e)
		}
		betIDs = append(betIDs, id)
		staked += stakeUusdc
	}

	// Run rounds until we reach tick, then cashout at target or wait for crash.
	cashoutSubmitted := make(map[uint64]bool, len(betIDs))
	// Safety: 200 blocks total (16 open + tick + 5 crashed).
	for step := 0; step < 200; step++ {
		c.AdvanceBlock()
		s := queryRound(c)

		// Submit cashout for any outstanding bet once mult meets target.
		// Each bet_action must be signed by the bet's own bettor — chainsim's
		// BetAction checks (bet.Bettor == addr) before calling WASM.
		if s.Phase == "tick" && s.MultBP >= targetMultBP {
			for idx, id := range betIDs {
				if cashoutSubmitted[id] {
					continue
				}
				b := c.GetBet(id)
				if b == nil || b.Status != chainsim.BetOpen {
					cashoutSubmitted[id] = true
					continue
				}
				if err := c.BetAction(players[idx], id, []byte{}); err == nil {
					cashoutSubmitted[id] = true
				}
			}
		}

		// Round done when phase is crashed and no open bets remain.
		if s.Phase == "crashed" {
			anyOpen := false
			for _, id := range betIDs {
				b := c.GetBet(id)
				if b != nil && b.Status == chainsim.BetOpen {
					anyOpen = true
					break
				}
			}
			if !anyOpen {
				break
			}
		}
	}

	for _, id := range betIDs {
		b := c.GetBet(id)
		if b != nil {
			paid += b.Payout
		}
	}
	return staked, paid, nil
}

// --- Section 1: house edge convergence ---

func runEdgeSection() section {
	// tick_growth is 350 bp per tick → multiplier grows 3.5%/tick starting at 1.0000.
	// Tick 0 multiplier = 10350, tick 1 = 10712, tick 2 = 11087, ... .
	// Cashout targets chosen to span reach probabilities:
	//   1.05  ~ close to first tick (very likely)
	//   1.20  ~ reached within ~6 ticks (~83%)
	//   1.50  ~ ~66%
	//   2.00  ~ ~49.5%
	//   3.00  ~ ~33%
	type cfg struct {
		name       string
		targetBP   uint64
		reachProb  float64 // approximate, for σ bound
	}
	configs := []cfg{
		{"cashout at 1.05x", 10500, 0.9429},
		{"cashout at 1.20x", 12000, 0.8250},
		{"cashout at 1.50x", 15000, 0.6600},
		{"cashout at 2.00x", 20000, 0.4950},
		{"cashout at 3.00x", 30000, 0.3300},
	}

	// Prepare player pool once.
	addrs := make([]string, betsPerRound)
	for i := range addrs {
		addrs[i] = fmt.Sprintf("p%d", i)
	}

	var results []result
	var tbl strings.Builder
	tbl.WriteString("| Strategy          | Target | Bets    | Realized edge | Tol (4σ) | Delta | Verdict |\n")
	tbl.WriteString("|-------------------|--------|---------|---------------|----------|-------|---------|\n")

	for _, k := range configs {
		chain := newChain(nil, bigHouseFunds)
		for _, a := range addrs {
			chain.Deposit(a, 1_000_000_000_000)
		}

		var staked, paid uint64
		failed := false
		for round := 0; round < edgeRoundsPer; round++ {
			s, p, err := playRound(chain, addrs, k.targetBP)
			if err != nil {
				results = append(results, result{
					name:   k.name,
					pass:   false,
					detail: fmt.Sprintf("round %d: %v", round, err),
				})
				failed = true
				break
			}
			staked += s
			paid += p
			if round%20 == 0 {
				chain.PurgeSettledBets()
			}
		}
		chain.Close()
		if failed {
			continue
		}

		totalBets := edgeRoundsPer * betsPerRound
		edgeBp := float64(int64(staked)-int64(paid)) / float64(staked) * 10000

		// Single-bet variance: X = mult_target w.p. reachProb, 0 w.p. 1-reachProb.
		// But bets in the same round are CORRELATED — they share the same tick
		// outcomes. σ is widened by that correlation; treat effective N = rounds.
		mult := float64(k.targetBP) / 10000
		varBet := k.reachProb*mult*mult - math.Pow(k.reachProb*mult, 2)
		effN := float64(edgeRoundsPer) // correlation ⇒ independent samples = rounds
		sigmaBp := math.Sqrt(varBet/effN) * 10000
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
		tbl.WriteString(fmt.Sprintf("| %-17s | %5.2fx | %7d | %9.1f bp  | %6.1f bp | %5.1f | %s |\n",
			k.name, mult, totalBets, edgeBp, tolBp, deltaBp, mark))
		results = append(results, result{
			name:   k.name,
			pass:   pass,
			detail: fmt.Sprintf("realized=%.1fbp tol(4σ)=%.1fbp delta=%.1fbp staked=%d paid=%d (N=%d bets across %d rounds)", edgeBp, tolBp, deltaBp, staked, paid, totalBets, edgeRoundsPer),
		})
	}

	return section{
		title:   fmt.Sprintf("House edge convergence (target %.2f%% / %dbp, 4σ dynamic tolerance, %d rounds × %d bets per config)", float64(houseEdgeBp)/100, houseEdgeBp, edgeRoundsPer, betsPerRound),
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

	// place_bet validation — only accepted during OPEN phase.

	runCase("place_bet during tick phase", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		if err := advanceTo(c, "tick", 40); err != nil {
			return err
		}
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		return err
	}, "status=10")

	runCase("place_bet during crashed phase", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		// Deposit many players so round can start and resolve naturally.
		c.Deposit("p1", 1_000_000_000)
		// Advance until a round reaches crashed.
		for i := 0; i < 300; i++ {
			c.AdvanceBlock()
			if queryRound(c).Phase == "crashed" {
				break
			}
		}
		if queryRound(c).Phase != "crashed" {
			return fmt.Errorf("never reached crashed phase")
		}
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		return err
	}, "status=10")

	runCase("stake=0", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		_, err := c.PlaceBet("p1", 1, 1, 0, []byte{})
		return err
	}, "stake must be > 0")

	runCase("stake below MinStakeUusdc", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		_, err := c.PlaceBet("p1", 1, 1, 1000, []byte{})
		return err
	}, "below minimum")

	runCase("stake > player balance", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("poor", 50_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		_, err := c.PlaceBet("poor", 1, 1, 1_000_000, []byte{})
		return err
	}, "insufficient balance")

	runCase("max_payout exceeds bankroll cap", func() error {
		p := chainsim.DefaultParams()
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", 10_000_000_000) // 10K USDC
		brID, _ := c.CreateBankroll("house", 10_000_000_000, "Tiny", false)
		c.RegisterGame(1, mustReadWasm(), "crash", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		// Crash max_multiplier defaults to 100x → stake 10M reserves 1B.
		// 2% cap on 10B bankroll = 200M → reject.
		_, err := c.PlaceBet("p1", brID, 1, 10_000_000, []byte{})
		return err
	}, "status=3")

	// bet_action validation — only accepted during TICK phase, on active bets.

	runCase("bet_action during open phase", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		// Still in open phase — bet_action should be rejected.
		return c.BetAction("p1", id, []byte{})
	}, "status=20")

	runCase("bet_action on already-cashed bet", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		if err := advanceTo(c, "open", 40); err != nil {
			return err
		}
		id, _ := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		if err := advanceTo(c, "tick", 40); err != nil {
			return err
		}
		// First cashout — should succeed.
		if err := c.BetAction("p1", id, []byte{}); err != nil {
			return fmt.Errorf("first cashout failed: %w", err)
		}
		// Drain the settlement.
		c.AdvanceBlock()
		// Second cashout — bet should be settled by now.
		return c.BetAction("p1", id, []byte{})
	}, "status=21")

	// Cross-cutting.

	runCase("bet on unattached game", func() error {
		c := chainsim.NewWithParams(chainsim.DefaultParams(), 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "crash", houseEdgeBp)
		// Skip AttachGame.
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", brID, 1, stakeUusdc, []byte{})
		return err
	}, "not active on bankroll")

	runCase("bet on killed calculator", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		_ = c.KillCalculator(1, "test")
		c.Deposit("p1", 1_000_000_000)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		return err
	}, "not active")

	runCase("bet while beacon down", func() error {
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		c.Deposit("p1", 1_000_000_000)
		c.SetBeaconAvailable(false)
		_, err := c.PlaceBet("p1", 1, 1, stakeUusdc, []byte{})
		return err
	}, "beacon randomness unavailable")

	return section{title: "Wrong-player behavior (rejection semantics)", results: results}
}

// --- Section 3: gas ---

func runGasSection() section {
	var results []result

	// 3.1 Initial gas balance.
	{
		c := newChain(nil, bigHouseFunds)
		want := c.Params().GasInitialCredits
		got := c.GasBalance(1)
		pass := got == want
		detail := fmt.Sprintf("want=%d got=%d", want, got)
		c.Close()
		results = append(results, result{name: "initial gas balance = params.GasInitialCredits", pass: pass, detail: detail})
	}

	// 3.2 Per-round gas is O(1) in history (crash "round" = many blocks).
	{
		c := newChain(nil, bigHouseFunds)
		addrs := []string{"p1", "p2", "p3"}
		for _, a := range addrs {
			c.Deposit(a, 1_000_000_000_000)
		}
		sample := func(n int) uint64 {
			var total uint64
			for i := 0; i < n; i++ {
				before := c.WasmGasUsed(1)
				if _, _, err := playRound(c, addrs, 12000); err != nil {
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
		at20 := sample(20)
		at100 := sample(100)
		at300 := sample(300)
		c.Close()
		if at20 == 0 {
			results = append(results, result{name: "per-round gas is O(1) in history", pass: false, detail: "no gas observed at n=20"})
		} else {
			grew := float64(at300) / float64(at20)
			pass := grew < 1.40 // crash rounds have higher variance than dice; loosen a bit
			detail := fmt.Sprintf("avg WASM gas/round: 20→%d, 100→%d, 300→%d (300/20 = %.2fx)", at20, at100, at300, grew)
			results = append(results, result{name: "per-round gas is O(1) in history", pass: pass, detail: detail})
		}
	}

	// 3.3 Balance invariant.
	{
		c := newChain(nil, bigHouseFunds)
		defer c.Close()
		initial := c.GasBalance(1)
		credit := c.Params().GasCreditPerBet
		addrs := []string{"p1", "p2", "p3"}
		for _, a := range addrs {
			c.Deposit(a, 1_000_000_000_000)
		}
		const n = 30
		totalBets := 0
		for i := 0; i < n; i++ {
			_, _, _ = playRound(c, addrs, 12000)
			totalBets += len(addrs)
		}
		final := c.GasBalance(1)
		ceiling := initial + uint64(totalBets)*credit
		pass := final <= ceiling && final > 0
		detail := fmt.Sprintf("initial=%d final=%d ceiling(initial+%d·credit)=%d", initial, final, totalBets, ceiling)
		results = append(results, result{name: "gas balance stays under (initial + N·credit)", pass: pass, detail: detail})
	}

	// 3.4 Gas exhaustion kills the calculator.
	// Crash has heavy block_update logic — budget exhausts quickly with no top-up.
	{
		p := chainsim.DefaultParams()
		p.GasInitialCredits = 500_000
		p.GasCreditPerBet = 0
		c := chainsim.NewWithParams(p, 42)
		defer c.Close()
		c.Deposit("house", bigHouseFunds)
		brID, _ := c.CreateBankroll("house", bigHouseFunds, "Test", false)
		c.RegisterGame(1, mustReadWasm(), "crash", houseEdgeBp)
		c.AttachGame(brID, 1)
		c.InitGame(1, brID)
		c.Deposit("p1", 1_000_000_000)
		var killedAt int
		for i := 0; i < 300; i++ {
			c.AdvanceBlock()
			calc, _ := c.GetCalculator(1)
			if calc != nil && calc.Status == chainsim.CalcStatusKilled {
				killedAt = i + 1
				break
			}
		}
		calc, _ := c.GetCalculator(1)
		pass := calc != nil && calc.Status == chainsim.CalcStatusKilled
		detail := fmt.Sprintf("killedAt=%d status=%d (block_update consumed gas until kill)", killedAt, statusCode(calc))
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
	fmt.Fprintf(&b, "\n=== Crash unified test suite ===\n\n")
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
	fmt.Fprintf(&b, "# Crash\n\n")
	fmt.Fprintf(&b, "Multiplayer crash — rising multiplier with random crash point.\n\n")
	fmt.Fprintf(&b, "**House edge: %.2f%%** (%d bp) applied at the first tick; subsequent ticks are fair (survival probability = previous_mult / next_mult).\n\n", float64(houseEdgeBp)/100, houseEdgeBp)
	fmt.Fprintf(&b, "**Source:** [`src/main.go`](./src/main.go) · **Binary:** [`crash.wasm`](./crash.wasm) · **Tests:** [`main.go`](./main.go)\n\n")
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
	return fmt.Sprintf("crash tests: passed=%d total=%d\n", passed, total)
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
