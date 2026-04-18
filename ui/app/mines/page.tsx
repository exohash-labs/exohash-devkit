"use client";

import { useState, useEffect, useCallback, useRef, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { Bomb, Loader2, Gem, X, Shield } from "lucide-react";
import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import { ExoScanBar, markBetPlaced } from "@/components/ExoScanBar";
import { PagePreloader } from "@/components/PagePreloader";
import { useWallet } from "@/contexts/WalletContext";
import { useStream } from "@/lib/useStream";
import { useBetFeed } from "@/lib/useBetFeed";
import { bff, formatUSDC, getUSDCBalance, type StatusResponse } from "@/lib/bff";
import { ensureRelayGrant } from "@/lib/signer";

// idle → placing → playing ⇄ broadcasting → playing | done
type GamePhase = "idle" | "placing" | "playing" | "broadcasting" | "done";

function humanizeError(raw: string): string {
  if (/status=10/i.test(raw) || /rejected bet/i.test(raw))
    return "Game rejected the bet. Try again.";
  if (/status=20/i.test(raw) || /status=21/i.test(raw) || /rejected action/i.test(raw) || /bet_id:\d+.*action:/i.test(raw))
    return "Action rejected — bet may already be settled.";
  if (/insufficient funds/i.test(raw) || /smaller than/i.test(raw)) {
    const m = raw.match(/spendable balance (\d+)uusdc/);
    if (m) return `Insufficient balance (${(parseInt(m[1]) / 1e6).toFixed(2)} USDC). Lower your stake.`;
    return "Insufficient balance. Lower your stake or get more funds.";
  }
  if (/account.*not found/i.test(raw)) return "Account not ready. Use the faucet first.";
  if (/timed out/i.test(raw) || /timeout/i.test(raw)) return "Transaction timed out. Please try again.";
  if (/exceeds max cap/i.test(raw) || /entry risk/i.test(raw) || /solvency/i.test(raw))
    return "Bankroll can't cover this bet. Lower your stake.";
  if (/beacon.*unavailable/i.test(raw)) return "Games temporarily paused";
  return raw.replace(/failed to execute message; message index: \d+: /g, "").replace(/tx [A-F0-9]+: tx failed code=\d+: /g, "");
}

type MinesBet = {
  betId: number;
  bettor: string;
  stake: string;
  payout: string;
  result: "win" | "loss" | "refund";
  mines?: number;
  revealed?: number;
  multBp?: number;
  ts: number;
};

function MinesGame() {
  const searchParams = useSearchParams();
  const bankrollId = Number(searchParams.get("bankroll") || process.env.NEXT_PUBLIC_BANKROLL_ID || "1");
  const gameId = Number(searchParams.get("game") || "3");

  const { ready: walletReady, status: walletStatus, address, wallet, openModal } = useWallet();
  const stream = useStream(gameId);

  // Fetch game info from BFF — direct houseEdgeBp (same fix as dice)
  const [houseEdgeBp, setHouseEdgeBp] = useState(200); // default 2%
  const [gameReady, setGameReady] = useState(false);
  const [gameStatus, setGameStatus] = useState(0);
  useEffect(() => {
    bff.games().then((games) => {
      const g = games.find((g) => g.id === gameId);
      if (g?.houseEdgeBp) setHouseEdgeBp(g.houseEdgeBp);
      if (g?.status) setGameStatus(g.status);
      setGameReady(true);
    }).catch(() => { setGameReady(true); });
  }, [gameId]);

  const houseEdgePct = (houseEdgeBp / 100).toFixed(1).replace(/\.0$/, "");

  // Track recent mines settlements from SSE — useBetFeed (ref + counter, no batching issues)
  const recentBets = useBetFeed<MinesBet>(gameId, ["settled"], (_ce, d) => ({
    betId: d.bet_id,
    bettor: d.addr || "",
    stake: String(d.stake || "0"),
    payout: String(d.payout || "0"),
    result: d.kind === 3 ? "refund" as const : d.kind === 1 ? "win" as const : "loss" as const,
    mines: d.mines,
    revealed: d.revealed,
    multBp: d.mult_bp,
    ts: Date.now(),
  }));

  // Game config
  const [mineCount, setMineCount] = useState(5);
  const [stakeInput, setStakeInput] = useState("1.00");

  // Game state
  const [phase, setPhase] = useState<GamePhase>("idle");
  const [betId, setBetId] = useState<number | null>(null);
  const [waitingForBetId, setWaitingForBetId] = useState(false);
  const [board, setBoard] = useState<(null | "safe" | "mine")[]>(
    Array(25).fill(null)
  );
  const [safeCount, setSafeCount] = useState(0);
  const [currentMultiplier, setCurrentMultiplier] = useState(1.0);
  const [result, setResult] = useState<any>(null);
  const [pendingTile, setPendingTile] = useState<number | null>(null);
  const [pendingAction, setPendingAction] = useState<"open" | "cashout" | null>(null);
  const [showVerify, setShowVerify] = useState(false);

  // Balance
  const [balanceUusdc, setBalanceUusdc] = useState("0");
  const [bankrollBalance, setBankrollBalance] = useState("0");
  const [maxPayoutCapBps, setMaxPayoutCapBps] = useState(200);
  const [bankrollInfo, setBankrollInfo] = useState<{
    name: string; creator: string; isPrivate: boolean; available: string;
  } | null>(null);
  const [error, setError] = useState("");
  const [needsGrant, setNeedsGrant] = useState(false);
  const [granting, setGranting] = useState(false);
  const [needsFaucet, setNeedsFaucet] = useState(false);
  const [faucetLoading, setFaucetLoading] = useState(false);

  // Fetch bankroll balance from chain (same as dice)
  useEffect(() => {
    fetch("/api/chain/house/types/bankrolls")
      .then(r => r.json())
      .then(data => {
        const br = data.views?.find((v: any) => v.bankroll?.id === String(bankrollId));
        if (br) {
          setBankrollBalance(br.balance);
          if (br.bankroll?.max_payout_cap_bps) setMaxPayoutCapBps(br.bankroll.max_payout_cap_bps);
          setBankrollInfo({
            name: br.bankroll?.name || "Pool",
            creator: br.bankroll?.creator || "",
            isPrivate: br.bankroll?.is_private || false,
            available: br.available || br.balance,
          });
        }
      })
      .catch(() => {});
  }, [bankrollId]);

  // Preview (multiplier table)
  const [multiplierTable, setMultiplierTable] = useState<number[]>([]);

  // Action generation counter — increments each time we start an action.
  const actionStartTime = useRef<number>(0);

  // Resolve betId from SSE "joined" event matching our address
  useEffect(() => {
    if (!waitingForBetId || !address || !stream.lastEvent?.calcEvents) return;
    for (const ce of stream.lastEvent.calcEvents) {
      if (ce.calculatorId !== gameId || ce.topic !== "joined") continue;
      try {
        const d = JSON.parse(ce.data);
        if (d.addr === address && d.bet_id) {
          setBetId(d.bet_id);
          setWaitingForBetId(false);
          setPhase("playing");
          console.log(`[mines] betId resolved from SSE: ${d.bet_id}`);
          return;
        }
      } catch {}
    }
  }, [stream.lastEvent?.height, waitingForBetId, address, gameId]);

  // SSE calcEvent processing — handles reveal, settled, joined.
  // This is the sole game state driver for the user's bet.
  const lastProcessedHeight = useRef(0);
  useEffect(() => {
    if (!betId || !stream.lastEvent?.calcEvents) return;
    if (stream.lastEvent.height <= lastProcessedHeight.current) return;
    lastProcessedHeight.current = stream.lastEvent.height;

    for (const ce of stream.lastEvent.calcEvents) {
      if (ce.calculatorId !== gameId) continue;
      let d: any;
      try { d = JSON.parse(ce.data); } catch { continue; }
      if (d.bet_id !== betId) continue;

      switch (ce.topic) {
        case "reveal": {
          const elapsed = actionStartTime.current ? (performance.now() - actionStartTime.current).toFixed(0) : "?";
          console.log(`[mines] click → reveal tile ${d.tile}: ${elapsed}ms`);

          if (d.safe === 1) {
            setBoard(prev => {
              const b = [...prev];
              b[d.tile] = "safe";
              return b;
            });
            const newSafe = d.revealed || safeCount + 1;
            setSafeCount(newSafe);
            if (d.mult_bp) setCurrentMultiplier(d.mult_bp / 10000);
            setPhase("playing");
            setPendingTile(null);
            setPendingAction(null);
          } else {
            // Mine hit — game over immediately
            setBoard(prev => {
              const b = [...prev];
              b[d.tile] = "mine";
              return b;
            });
            setPhase("done");
            setPendingTile(null);
            setPendingAction(null);
          }
          break;
        }

        case "settled": {
          const isWin = d.kind === 1;
          const isRefund = d.kind === 3;
          const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
          setResult({
            id: betId,
            bankrollId,
            gameId,
            engine: "",
            bettor: address || "",
            stake: { denom: "uusdc", amount: String(stakeUusdc) },
            phase: "GAME_PHASE_DONE",
            result: {
              win: isWin,
              payout: String(d.payout || 0) + "uusdc",
              reason: d.reason,
            },
          });
          setPhase("done");
          setPendingTile(null);
          setPendingAction(null);
          if (address) bff.balance(address).then((b) => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
          break;
        }
      }
    }
  }, [stream.lastEvent?.height, betId, gameId, safeCount, address, stakeInput, bankrollId]);

  // Fallback: detect settlement from betsSettled (for timeout/auto-settle when idle).
  useEffect(() => {
    if (phase !== "playing" || !betId || !stream.lastEvent?.settlements) return;
    const settlement = stream.lastEvent.settlements.find((s: any) => s.betId === betId);
    if (!settlement) return;
    setResult({
      id: settlement.betId,
      bankrollId: settlement.bankrollId,
      gameId: settlement.gameId,
      engine: "",
      bettor: settlement.bettor,
      stake: { denom: "uusdc", amount: String(settlement.stake || 0) },
      phase: "GAME_PHASE_DONE",
      result: { win: Number(settlement.payout || 0) > 0, payout: (settlement.payout || "0") + "uusdc" },
    });
    setPhase("done");
    setPendingAction(null);
    if (address) bff.balance(address).then((bal) => setBalanceUusdc(getUSDCBalance(bal.balances))).catch(() => {});
  }, [stream.lastEvent?.height, phase, betId, address]);

  // Cold start: restore active mines bet on mount.
  const coldStartDone = useRef(false);
  useEffect(() => {
    if (!address || coldStartDone.current) return;
    coldStartDone.current = true;

    bff.playerBets(address, 10).then(data => {
      const bets = Array.isArray(data) ? data : data?.bets;
      if (!bets) return;
      const openMines = bets.find((b: any) => b.gameId === gameId && b.status === "open");
      if (!openMines) return;
      const id = openMines.betId ?? (openMines as any).betId;
      if (!id) return;

      // Fetch bet state with calc events to restore board
      bff.bet(id).then((state: any) => {
        if (state.status !== "open") return;
        setBetId(id);
        setPhase("playing");

        // Replay events to rebuild board
        const newBoard: (null | "safe" | "mine")[] = Array(25).fill(null);
        let revealed = 0;
        let mult = 1.0;
        let mines = mineCount;
        for (const ev of state.events || []) {
          try {
            const d = JSON.parse(ev.data);
            if (ev.topic === "joined") {
              mines = d.mines || mines;
            } else if (ev.topic === "reveal" && d.safe === 1) {
              newBoard[d.tile] = "safe";
              revealed = d.revealed || revealed + 1;
              if (d.mult_bp) mult = d.mult_bp / 10000;
            }
          } catch {}
        }
        setBoard(newBoard);
        setSafeCount(revealed);
        setCurrentMultiplier(mult);
        setMineCount(mines);
      }).catch(() => {});
    }).catch(() => {});
  }, [address, gameId, mineCount]);

  useEffect(() => {
    if (!address) return;
    const fetch = () =>
      bff
        .balance(address)
        .then((b) => setBalanceUusdc(getUSDCBalance(b.balances)))
        .catch(() => {});
    fetch();
    const id = setInterval(fetch, 5000);
    return () => clearInterval(id);
  }, [address]);

  // Compute multiplier table client-side (same math as WASM)
  useEffect(() => {
    const board = 25;
    const safe = board - mineCount;
    const maxReveals = Math.min(5, safe);
    const houseEdge = houseEdgeBp / 10000;
    const table: number[] = [];
    let num = 1;
    let den = 1;
    for (let k = 1; k <= maxReveals; k++) {
      num *= (board - k + 1);
      den *= (safe - k + 1);
      const fairMult = num / den;
      table.push(fairMult * (1 - houseEdge));
    }
    setMultiplierTable(table);
  }, [mineCount, houseEdgeBp]);

  // Place initial bet
  const handleStart = useCallback(async () => {
    if (walletStatus !== "unlocked" || !wallet || !address) {
      openModal();
      return;
    }
    const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
    if (stakeUusdc < 10000) {
      setError("Minimum stake is $0.01");
      return;
    }
    setError("");
    setPhase("placing");
    const t0 = performance.now();

    try {
      const result = await bff.relayPlaceBet({
        address,
        bankrollId,
        gameId,
        stake: stakeUusdc.toString(),
        gameState: [mineCount],
      });
      console.log(`[mines] click → tx confirmed: ${(performance.now() - t0).toFixed(0)}ms`);
      if (address) markBetPlaced(address);
      // betId will be set from SSE "joined" event matching our address
      setWaitingForBetId(true);
      setPhase("placing");
    } catch (e: any) {
      const msg = e.message || "Failed to start";
      if (msg.includes("authorization not found") || msg.includes("unauthorized")) {
        setNeedsGrant(true);
        setError("Instant betting not authorized. Click 'Authorize' below.");
      } else if (/insufficient funds|smaller than|account.*not found|not on chain|spendable balance/i.test(msg)) {
        setNeedsFaucet(true);
        setError(humanizeError(msg));
      } else {
        setError(humanizeError(msg));
      }
      setPhase("idle");
    }
  }, [walletStatus, address, stakeInput, bankrollId, gameId, mineCount, openModal]);

  const resetToIdle = useCallback(() => {
    setBetId(null);
    setBoard(Array(25).fill(null));
    setSafeCount(0);
    setCurrentMultiplier(1.0);
    setResult(null);
    setPendingAction(null);
    setError("");
    setPhase("idle");
  }, []);

  const resetAndPlay = useCallback(() => {
    setBetId(null);
    setBoard(Array(25).fill(null));
    setSafeCount(0);
    setCurrentMultiplier(1.0);
    setResult(null);
    setPendingAction(null);
    setError("");
    setPhase("idle");
    setTimeout(() => handleStart(), 0);
  }, [handleStart]);

  // Open a tile
  const actionInFlight = useRef(false);
  const handleOpenTile = useCallback(
    async (tileIndex: number) => {
      if (phase !== "playing" || !address || !betId) return;
      if (board[tileIndex] !== null) return;
      if (actionInFlight.current) return;

      actionInFlight.current = true;
      actionStartTime.current = performance.now();
      setPhase("broadcasting");
      setPendingTile(tileIndex);
      setPendingAction("open");
      setError("");

      try {
        await bff.relayGameAction({
          address,
          betId,
          action: [1, tileIndex],
        });
        // TX confirmed. Stay in "broadcasting" until SSE "reveal" arrives —
        // SSE is the sole phase driver. Flipping to "playing" here briefly
        // re-renders the CTA panel (OPEN A TILE / CASH OUT) before the board
        // updates, causing a flash.
        actionInFlight.current = false;
      } catch (e: any) {
        const msg = e.message || "";
        const wasSettled = /bet_id.*action|rejected action|status=2[01]/i.test(msg);
        // Bet already resolved (mine hit during broadcast) — let SSE deliver
        // the settled event; don't revert the UI to "playing" in the meantime.
        if (!wasSettled) {
          setError(humanizeError(msg || "Action failed"));
          setPhase("playing");
          setPendingTile(null);
          setPendingAction(null);
        }
        actionInFlight.current = false;
      }
    },
    [phase, address, betId, board]
  );

  // Cashout
  const handleCashout = useCallback(async () => {
    if (phase !== "playing" || !address || !betId || safeCount === 0)
      return;

    actionStartTime.current = performance.now();
    setPhase("broadcasting");
    setPendingTile(null);
    setPendingAction("cashout");
    setError("");

    try {
      await bff.relayGameAction({
        address,
        betId,
        action: [2],
      });
      // TX confirmed, stay in broadcasting until SSE "settled" arrives
    } catch (e: any) {
      const msg = e.message || "";
      const wasSettled = /bet_id.*action|rejected action|status=2[01]/i.test(msg);
      if (!wasSettled) {
        setError(humanizeError(msg || "Cashout failed"));
        setPhase("playing");
        setPendingAction(null);
      }
    }
  }, [phase, wallet, address, betId, safeCount]);

  const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
  const payoutAmount = parseInt((result?.result?.payout || "0").replace("uusdc", "")) || 0;
  const isRefund = result?.result?.win === true && (result?.result?.multiplier_bp === 10000 || payoutAmount === stakeUusdc);
  const isWin = result?.result?.win === true && !isRefund;
  const busy = phase === "broadcasting";

  // Chance calculation for stats
  const totalTiles = 25;
  const safeTiles = totalTiles - mineCount;
  const chanceFirstTile = safeTiles > 0 ? ((safeTiles / totalTiles) * 100) : 0;
  const nextChance = safeTiles > safeCount ? (((safeTiles - safeCount) / (totalTiles - safeCount)) * 100) : 0;

  if (gameStatus === 2) return <PagePreloader message="Mines is temporarily unavailable. The game calculator was stopped by the system." />;
  if (gameStatus === 1) return <PagePreloader message="Mines is currently paused by the bankroll operator." />;
  if (!walletReady || !gameReady) return <PagePreloader />;

  return (
    <div className="min-h-screen flex flex-col relative page-bg">

      <Header balance={balanceUusdc} />
      <ExoScanBar />

      {/* ── MAIN ── */}
      <main className="flex-1 relative z-10 pt-[56px]">
        <div className="max-w-[960px] mx-auto px-4 sm:px-6">

          {/* ── Centered breadcrumb + heading ── */}
          <section className="pt-6 pb-4 text-center">
            <h1 className="text-6xl font-black text-white leading-none mb-2 font-[family-name:var(--font-title)] tracking-wider neon-gold">
              MINES
            </h1>
            <p className="text-[12px] text-white font-[family-name:var(--font-display)] flex items-center justify-center gap-1.5 flex-wrap">
              <span>RTP: {(100 - parseFloat(houseEdgePct)).toFixed(0)}%</span>
              <span>·</span>
              <span>Max Payout per Bet: ${formatUSDC(String(Math.floor(Number(bankrollBalance) * maxPayoutCapBps / 10000)))}</span>
              <span>·</span>
              <a href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/games/${gameId}`} target="_blank" rel="noreferrer" className="text-purple-400 hover:text-yellow-400 transition-colors">
                Verify game →
              </a>
            </p>
          </section>

          {/* ── Game area ── */}
          <section className="pb-8">
            <div className="flex flex-col lg:flex-row gap-6">

              {/* ── LEFT: Board ── */}
              <div className="flex-1 min-w-0">
                <div className="glass-card rounded-3xl p-6 sm:p-8">
                  <div className="grid grid-cols-5 gap-2 sm:gap-2.5 max-w-[370px] mx-auto w-full">
                    {board.map((tile, i) => {
                      const isRevealing = pendingTile === i && busy;
                      return (
                        <button
                          key={i}
                          onClick={() => handleOpenTile(i)}
                          disabled={phase !== "playing" || tile !== null || busy}
                          className={`mines-tile aspect-square rounded-xl text-sm font-bold transition-all duration-200 flex items-center justify-center ${
                            tile === "safe"
                              ? "mines-tile-safe"
                              : tile === "mine"
                              ? "mines-tile-mine"
                              : isRevealing
                              ? "mines-tile-pending"
                              : phase === "playing" && tile === null
                              ? "hover:border-yellow-400/40 hover:bg-yellow-400/5 cursor-pointer active:scale-95"
                              : "mines-tile-disabled"
                          }`}
                        >
                          {tile === "safe" ? (
                            <Gem className="w-6 h-6 sm:w-7 sm:h-7 text-yellow-400 drop-shadow-[0_0_12px_rgba(250,204,21,0.6)]" />
                          ) : tile === "mine" ? (
                            <Bomb className="w-6 h-6 sm:w-7 sm:h-7 text-red-400 drop-shadow-[0_0_12px_rgba(248,113,113,0.6)]" />
                          ) : isRevealing ? (
                            <Loader2 className="w-5 h-5 animate-spin text-yellow-400" />
                          ) : null}
                        </button>
                      );
                    })}
                  </div>

                  {/* Multiplier step bar */}
                  {multiplierTable.length > 0 && (
                    <div className="mt-4 grid grid-cols-5 gap-1.5 max-w-[370px] mx-auto w-full">
                      {multiplierTable.map((m, i) => {
                        const isCompleted = i < safeCount;
                        const isCurrent = i === safeCount && phase !== "idle" && phase !== "done";
                        const isNext = i === safeCount && phase === "idle";
                        return (
                          <div
                            key={i}
                            className={`flex flex-col items-center py-1.5 rounded-lg text-center transition-all ${
                              isCompleted
                                ? "bg-emerald-500/20 border border-emerald-500/40 shadow-[0_0_10px_rgba(52,211,153,0.3)]"
                                : isCurrent
                                ? "bg-yellow-400/20 border border-yellow-400/50 shadow-[0_0_10px_rgba(234,179,8,0.4)] scale-105 mines-step-active"
                                : isNext
                                ? "bg-zinc-900/40 border border-purple-500/15"
                                : "bg-zinc-900/40 border border-purple-500/15"
                            }`}
                          >
                            <div className={`text-[9px] font-bold font-[family-name:var(--font-display)] ${
                              isCompleted ? "text-emerald-400" : isCurrent ? "text-yellow-400" : "text-zinc-500"
                            }`}>
                              {i + 1}
                            </div>
                            <div className={`text-[11px] sm:text-sm font-black font-[family-name:var(--font-title)] ${
                              isCompleted ? "text-emerald-400" : isCurrent || isNext ? "text-yellow-400" : "text-zinc-600"
                            }`}>
                              {m.toFixed(2)}x
                            </div>
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              </div>

              {/* ── RIGHT: Controls ── */}
              <div className="w-full lg:w-[340px] shrink-0 glass-panel rounded-3xl p-5 sm:p-6 flex flex-col gap-4">

                  {/* Number of mines */}
                  <div className="glass-panel rounded-3xl p-5">
                    <div className="text-[12px] tracking-[0.25em] text-yellow-300 uppercase font-bold font-[family-name:var(--font-display)] mb-2">
                      NUMBER OF MINES
                    </div>
                    <div className="flex items-center gap-3 mb-1">
                      <input type="range" min={1} max={13} value={mineCount}
                        onChange={(e) => setMineCount(Number(e.target.value))}
                        disabled={phase !== "idle" && phase !== "done"}
                        className="flex-1 h-2 rounded-lg appearance-none cursor-pointer bg-purple-900/40 accent-yellow-400"
                        style={{ accentColor: '#facc15' }} />
                      <div className="w-12 text-center text-xl font-black text-yellow-400 font-[family-name:var(--font-title)]" style={{ textShadow: '0 0 15px rgba(234,179,8,0.3)' }}>
                        {mineCount}
                      </div>
                    </div>
                  </div>


                  {/* Stake selector */}
                  <div>
                    <div className="text-[10px] tracking-[0.25em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-2">
                      STAKE
                    </div>
                    <div className="grid grid-cols-5 gap-1.5">
                      {["0.50", "1.00", "2.00", "5.00", "10.00"].map(v => (
                        <button key={v} onClick={() => setStakeInput(v)} disabled={phase !== "idle" && phase !== "done"}
                          className={`py-2.5 rounded-2xl text-xs font-bold transition-all cursor-pointer font-[family-name:var(--font-display)] ${
                            stakeInput === v
                              ? "stake-chip-active"
                              : "bg-zinc-800 border border-yellow-400/30 text-zinc-400 hover:text-white hover:border-yellow-400/50"
                          } disabled:opacity-50 disabled:cursor-not-allowed`}>
                          ${v.replace(/\.00$/, "")}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Action button */}
                  <div className="flex flex-col gap-2.5">
                    {/* Error */}
                    {error && (
                      <div className="bg-red-500/10 border border-red-500/20 rounded-2xl px-4 py-2.5">
                        <p className="text-xs text-red-400 text-center font-[family-name:var(--font-display)]">{error}</p>
                      </div>
                    )}

                    {/* Faucet */}
                    {needsFaucet && address && (
                      <button onClick={async () => {
                          setFaucetLoading(true);
                          try {
                            await bff.faucet(address);
                            setNeedsFaucet(false); setError("");
                            await new Promise(r => setTimeout(r, 2000));
                            bff.balance(address).then(b => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
                            if (wallet) ensureRelayGrant(wallet, address).catch(() => {});
                          } catch { setError("Faucet error — try again"); } setFaucetLoading(false);
                        }} disabled={faucetLoading}
                        className="w-full py-3 border border-emerald-500/30 bg-emerald-500/10 text-emerald-400 rounded-2xl text-sm font-bold hover:bg-emerald-500/20 transition-colors disabled:opacity-50 cursor-pointer flex items-center justify-center gap-2 font-[family-name:var(--font-display)]">
                        {faucetLoading ? "Sending tokens..." : "Get Test USDC"}
                      </button>
                    )}

                    {/* Grant */}
                    {needsGrant && wallet && address && (
                      <button onClick={async () => {
                          setGranting(true); setError("");
                          try { const relayInfo = await bff.relayInfo();
                            if (relayInfo.enabled) { const { grantRelay } = await import("@/lib/signer");
                              const res = await grantRelay(wallet, address, relayInfo.relayAddress);
                              if (res.code === 0) { setNeedsGrant(false); setError(""); } else { setError("Authorization failed. Try again."); }
                            }
                          } catch (e: any) { setError(e.message?.slice(0, 100) || "Authorization failed"); } setGranting(false);
                        }} disabled={granting}
                        className="w-full py-3 border border-yellow-400/30 bg-yellow-400/10 text-yellow-400 rounded-2xl text-sm font-bold hover:bg-yellow-400/20 transition-colors disabled:opacity-50 cursor-pointer flex items-center justify-center gap-2 font-[family-name:var(--font-display)]">
                        {granting ? "Authorizing..." : "Authorize Instant Betting"}
                      </button>
                    )}

                    {phase === "idle" && (
                      <button onClick={handleStart}
                        className="w-full mt-3 btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer flex items-center justify-center gap-2">
                        {walletStatus === "none" ? "CREATE A WALLET" : walletStatus === "locked" ? "UNLOCK YOUR WALLET" : "START GAME"}
                      </button>
                    )}

                    {phase === "placing" && (
                      <button disabled
                        className="w-full mt-3 btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-purple-300 font-bold flex items-center justify-center gap-2 tracking-widest">
                        <Loader2 className="w-5 h-5 animate-spin" /> STARTING...
                      </button>
                    )}

                    {phase === "playing" && safeCount === 0 && !busy && (
                      <div className="w-full mt-3 btn-muted rounded-2xl py-4 text-center text-lg font-[family-name:var(--font-title)] font-bold tracking-wider uppercase">
                        OPEN A TILE
                      </div>
                    )}

                    {phase === "playing" && safeCount > 0 && !busy && (
                      <button onClick={handleCashout}
                        className="w-full mt-3 btn-action rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-black font-bold cursor-pointer flex flex-col items-center justify-center tracking-widest">
                        <span>CASH OUT ${formatUSDC(Math.floor(stakeUusdc * currentMultiplier).toString())} USDC</span>
                        <span className="text-xs font-normal opacity-80">(+${((stakeUusdc * currentMultiplier - stakeUusdc) / 1e6).toFixed(2)} profit)</span>
                      </button>
                    )}

                    {busy && (
                      <button disabled
                        className="w-full mt-3 btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-purple-300 font-bold flex items-center justify-center gap-2 tracking-widest">
                        <Loader2 className="w-5 h-5 animate-spin" /> WAITING FOR CHAIN...
                      </button>
                    )}

                    {phase === "done" && result && (
                      <div className="mt-3 space-y-3">
                        <div className={`text-center py-3 rounded-2xl border ${
                          isRefund ? "border-yellow-400/30 bg-yellow-400/5" : isWin ? "border-emerald-500/30 bg-emerald-500/5" : "border-red-500/30 bg-red-500/5"
                        }`}>
                          <div className={`text-xl font-black font-[family-name:var(--font-title)] tracking-wider ${
                            isRefund ? "text-yellow-400" : isWin ? "text-emerald-400" : "text-red-400"
                          }`}>
                            {isRefund ? "TIMED OUT" : isWin ? "CASHED OUT!" : "BOOM!"}
                          </div>
                          <div className={`text-sm font-[family-name:var(--font-display)] ${
                            isRefund ? "text-yellow-400/70" : isWin ? "text-emerald-400/70" : "text-red-400/70"
                          }`}>
                            {isRefund ? "Stake returned" : isWin
                              ? `+$${formatUSDC(String(Math.max(0, parseInt((result.result?.payout || "0").replace("uusdc", "")) - stakeUusdc)))} profit`
                              : `-$${formatUSDC(String(stakeUusdc))}`}
                          </div>
                        </div>
                        <button onClick={resetAndPlay}
                          className="w-full btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest cursor-pointer">
                          NEW GAME
                        </button>
                        {betId && (
                          <a href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bets/${gameId}/${betId}`} target="_blank" rel="noreferrer"
                            className="block text-center text-[11px] text-purple-400 hover:text-yellow-400 transition-colors font-[family-name:var(--font-display)]">
                            <Shield className="w-3 h-3 inline mr-1" />Verify on ExoScan
                          </a>
                        )}
                      </div>
                    )}
                  </div>
                </div>
            </div>
          </section>

          {/* ── BET TABLES: My Bets | All Bets ── */}
          <section className="pb-6">
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-5 max-w-[1340px] mx-auto">
              {/* ── MY BETS ── */}
              <div className="glass-panel rounded-3xl overflow-hidden">
                <div className="px-5 py-3 border-b border-purple-500/30 flex items-center gap-2.5">
                  <div className="w-2 h-2 rounded-full bg-yellow-400 live-dot" />
                  <h3 className="text-[11px] tracking-[0.25em] text-purple-300 uppercase font-bold font-[family-name:var(--font-display)]">
                    My Bets
                  </h3>
                  {address ? (
                    <a
                      href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bets?bettor=${address}`}
                      target="_blank"
                      rel="noreferrer"
                      className="text-[10px] text-zinc-400 font-mono ml-auto hover:text-yellow-400 transition-colors"
                      title="View all on ExoScan"
                    >
                      {address.slice(0, 8)}... ↗
                    </a>
                  ) : (
                    <span className="text-[10px] text-zinc-400 font-mono ml-auto">—</span>
                  )}
                </div>
                <div className="max-h-[320px] overflow-y-auto">
                  <table className="w-full text-[12px]">
                    <thead>
                      <tr className="text-[10px] tracking-[0.15em] text-purple-300 uppercase font-[family-name:var(--font-title)] border-b border-purple-500/30">
                        <th className="text-left px-3 py-2 font-bold">Mines</th>
                        <th className="text-right px-3 py-2 font-bold">Opened</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Profit</th>
                        <th className="text-right px-3 py-2 font-bold">Result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const myBets = recentBets.filter(b => b.bettor === address);
                        if (myBets.length === 0) return (
                          <tr><td colSpan={5} className="text-center py-6 text-zinc-500">No bets yet</td></tr>
                        );
                        return myBets.slice(0, 500).map((b, i) => {
                          const profit = b.result === "refund" ? 0 : b.result === "win" ? (parseInt(b.payout) - parseInt(b.stake)) / 1e6 : -(parseInt(b.stake) / 1e6);
                          return (
                          <tr key={b.betId} className={`border-b border-zinc-800 ${i === 0 ? "animate-in" : ""} hover:bg-purple-500/5 transition-colors`}>
                            <td className="px-3 py-2 font-[family-name:var(--font-display)] text-zinc-300">
                              {b.mines ?? "—"}
                            </td>
                            <td className="px-3 py-2 text-right font-[family-name:var(--font-display)] text-zinc-300">
                              {b.revealed ?? "—"}
                            </td>
                            <td className="px-3 py-2 text-right text-white font-bold font-[family-name:var(--font-display)]">
                              ${(parseInt(b.stake) / 1e6).toFixed(2)}
                            </td>
                            <td className={`px-3 py-2 text-right font-bold font-[family-name:var(--font-display)] ${b.result === "refund" ? "text-yellow-400" : profit >= 0 ? "text-emerald-400" : "text-red-400"}`}>
                              {b.result === "refund" ? "$0" : profit >= 0 ? `+$${profit.toFixed(2)}` : `-$${Math.abs(profit).toFixed(2)}`}
                            </td>
                            <td className="px-3 py-2 text-right">
                              <span className={`inline-block px-2.5 py-0.5 rounded-full text-[10px] font-bold tracking-wider ${
                                b.result === "win" ? "bg-emerald-500/20 text-emerald-400"
                                  : b.result === "refund" ? "bg-yellow-400/20 text-yellow-400"
                                  : "bg-red-500/20 text-red-400"
                              }`}>
                                {b.result === "win" ? "WIN" : b.result === "refund" ? "REFUND" : "LOSS"}
                              </span>
                            </td>
                          </tr>
                        );});
                      })()}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* ── ALL BETS ── */}
              <div className="glass-panel rounded-3xl overflow-hidden">
                <div className="px-5 py-3 border-b border-purple-500/30 flex items-center gap-2.5">
                  <div className="w-2 h-2 rounded-full bg-emerald-400 live-dot" />
                  <h3 className="text-[11px] tracking-[0.25em] text-purple-300 uppercase font-bold font-[family-name:var(--font-display)]">
                    All Bets
                  </h3>
                  <span className="text-[10px] text-zinc-400 font-mono ml-auto">
                    Live Feed
                  </span>
                </div>
                <div className="max-h-[320px] overflow-y-auto">
                  <table className="w-full text-[12px]">
                    <thead>
                      <tr className="text-[10px] tracking-[0.15em] text-purple-300 uppercase font-[family-name:var(--font-title)] border-b border-purple-500/30">
                        <th className="text-left px-3 py-2 font-bold">Player</th>
                        <th className="text-right px-3 py-2 font-bold">Mines</th>
                        <th className="text-right px-3 py-2 font-bold">Opened</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Profit</th>
                        <th className="text-right px-3 py-2 font-bold">Result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const otherBets = recentBets.filter(b => b.bettor !== address);
                        if (otherBets.length === 0) return (
                          <tr><td colSpan={6} className="text-center py-6 text-zinc-500">Waiting for bets...</td></tr>
                        );
                        return otherBets.slice(0, 500).map((b, i) => {
                          const profit = b.result === "refund" ? 0 : b.result === "win" ? (parseInt(b.payout) - parseInt(b.stake)) / 1e6 : -(parseInt(b.stake) / 1e6);
                          return (
                          <tr key={b.betId} className={`border-b border-zinc-800 ${i === 0 ? "animate-in" : ""} hover:bg-purple-500/5 transition-colors`}>
                            <td className="px-3 py-2.5 font-[family-name:var(--font-display)] text-purple-400 text-[10px]">
                              {b.bettor.slice(0, 6)}..{b.bettor.slice(-3)}
                            </td>
                            <td className="px-3 py-2 text-right font-[family-name:var(--font-display)] text-zinc-300">
                              {b.mines ?? "—"}
                            </td>
                            <td className="px-3 py-2 text-right font-[family-name:var(--font-display)] text-zinc-300">
                              {b.revealed ?? "—"}
                            </td>
                            <td className="px-3 py-2 text-right text-white font-bold font-[family-name:var(--font-display)]">
                              ${(parseInt(b.stake) / 1e6).toFixed(2)}
                            </td>
                            <td className={`px-3 py-2 text-right font-bold font-[family-name:var(--font-display)] ${b.result === "refund" ? "text-yellow-400" : profit >= 0 ? "text-emerald-400" : "text-red-400"}`}>
                              {b.result === "refund" ? "$0" : profit >= 0 ? `+$${profit.toFixed(2)}` : `-$${Math.abs(profit).toFixed(2)}`}
                            </td>
                            <td className="px-3 py-2 text-right">
                              <span className={`inline-block px-2.5 py-0.5 rounded-full text-[10px] font-bold tracking-wider ${
                                b.result === "win" ? "bg-emerald-500/20 text-emerald-400"
                                  : b.result === "refund" ? "bg-yellow-400/20 text-yellow-400"
                                  : "bg-red-500/20 text-red-400"
                              }`}>
                                {b.result === "win" ? "WIN" : b.result === "refund" ? "REFUND" : "LOSS"}
                              </span>
                            </td>
                          </tr>
                        );});
                      })()}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </section>

        </div>
      </main>

      <Footer />

    </div>
  );
}

export default function MinesPage() {
  return (
    <Suspense fallback={<PagePreloader />}>
      <MinesGame />
    </Suspense>
  );
}
