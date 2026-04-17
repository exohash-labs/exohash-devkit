"use client";

import { useState, useEffect, useCallback, useRef, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { Loader2, Rocket, Shield } from "lucide-react";
import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import { ExoScanBar, markBetPlaced } from "@/components/ExoScanBar";
import { PagePreloader } from "@/components/PagePreloader";
import { useWallet } from "@/contexts/WalletContext";
import { bff, formatUSDC, getUSDCBalance } from "@/lib/bff";
import { ensureRelayGrant } from "@/lib/signer";

// ── Helpers ──

function humanizeError(raw: string): string {
  if (/status=10/i.test(raw) || /round is closed/i.test(raw) || /round_phase_resolving/i.test(raw))
    return "Round already started — wait for the next one";
  if (/status=11/i.test(raw) || /already joined/i.test(raw))
    return "You already joined this round.";
  if (/status=20/i.test(raw) || /rejected action/i.test(raw) || /bet_id:\d+.*action:/i.test(raw))
    return "Round already crashed — cashout too late";
  if (/status=21/i.test(raw))
    return "Bet already settled";
  if (/insufficient funds/i.test(raw) || /smaller than/i.test(raw)) {
    const m = raw.match(/spendable balance (\d+)uusdc/);
    if (m) return `Insufficient balance (${(parseInt(m[1]) / 1e6).toFixed(2)} USDC). Lower your stake.`;
    return "Insufficient balance. Lower your stake or use the faucet.";
  }
  if (/account.*not found/i.test(raw)) return "Account not ready. Use the faucet first.";
  if (/exceeds max cap/i.test(raw) || /entry risk/i.test(raw) || /solvency/i.test(raw))
    return "Bankroll can't cover this bet. Lower your stake.";
  if (/beacon.*unavailable/i.test(raw)) return "Games temporarily paused";
  return raw.replace(/failed to execute message; message index: \d+: /g, "").replace(/tx [A-F0-9]+: tx failed code=\d+: /g, "");
}

// ── Types ──

type CrashPhase = "waiting" | "open" | "tick" | "crashed";

type BetEntry = {
  betId: number;
  bettor: string;
  round: number;
  stake: number;           // uusdc
  action: "joined" | "cashed" | "busted";
  multBp?: number;         // cashout mult (cashed) or crash mult (busted)
  payout?: number;         // uusdc
  mine?: boolean;          // true if this is the current user's bet
};

// ── Crash chart ──
// Draws the exponential curve f(t) = 1.035^t from tick 0 to current tick.
// TICK_GROWTH = 1.035 (from WASM tick_growth_bp=350), MAX_MULT = 100x.

const TICK_GROWTH = 1.035;
const MAX_MULT = 100;

// Total ticks for 1x→100x: log(100)/log(1.035) ≈ 134
const TOTAL_TICKS = Math.ceil(Math.log(MAX_MULT) / Math.log(TICK_GROWTH)); // ~134

function CrashChart({ phase, tick, multiplier }: { phase: CrashPhase; tick: number; multiplier: number }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const rect = canvas.getBoundingClientRect();
    canvas.width = rect.width * dpr;
    canvas.height = rect.height * dpr;
    ctx.scale(dpr, dpr);
    const W = rect.width, H = rect.height;
    const pad = { top: 20, right: 20, bottom: 30, left: 45 };
    const plotW = W - pad.left - pad.right;
    const plotH = H - pad.top - pad.bottom;

    ctx.clearRect(0, 0, W, H);

    // Fixed axes: full curve 0→TOTAL_TICKS, 1x→100x
    const getX = (t: number) => pad.left + (t / TOTAL_TICKS) * plotW;
    const getY = (m: number) => pad.top + plotH - ((m - 1) / (MAX_MULT - 1)) * plotH;

    // ── Y-axis labels ──
    ctx.textAlign = "right";
    ctx.textBaseline = "middle";
    ctx.font = "10px monospace";
    for (const m of [1, 2, 5, 10, 20, 50, 100]) {
      const y = getY(m);
      ctx.strokeStyle = "rgba(107, 33, 168, 0.1)";
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(pad.left, y); ctx.lineTo(W - pad.right, y); ctx.stroke();
      ctx.fillStyle = "rgba(161, 131, 200, 0.35)";
      ctx.fillText(`${m}x`, pad.left - 6, y);
    }

    // ── X-axis labels ──
    ctx.textAlign = "center";
    ctx.textBaseline = "top";
    for (let t = 0; t <= TOTAL_TICKS; t += 20) {
      const x = getX(t);
      ctx.strokeStyle = "rgba(107, 33, 168, 0.06)";
      ctx.lineWidth = 1;
      ctx.beginPath(); ctx.moveTo(x, pad.top); ctx.lineTo(x, H - pad.bottom); ctx.stroke();
      ctx.fillStyle = "rgba(161, 131, 200, 0.25)";
      ctx.fillText(`${t}`, x, H - pad.bottom + 6);
    }

    // ── Full static exponential curve (dim) ──
    const RESOLUTION = 2;
    const allPoints: { x: number; y: number }[] = [];
    for (let i = 0; i <= TOTAL_TICKS * RESOLUTION; i++) {
      const t = i / RESOLUTION;
      const m = Math.min(Math.pow(TICK_GROWTH, t), MAX_MULT);
      allPoints.push({ x: getX(t), y: getY(m) });
    }

    // Dim curve line
    ctx.beginPath();
    ctx.moveTo(allPoints[0].x, allPoints[0].y);
    for (let i = 1; i < allPoints.length; i++) ctx.lineTo(allPoints[i].x, allPoints[i].y);
    ctx.strokeStyle = "rgba(107, 33, 168, 0.3)";
    ctx.lineWidth = 2;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    ctx.stroke();

    // ── Active portion (up to current tick) — bright ──
    const crashed = phase === "crashed";
    const active = phase === "tick" || crashed;
    if (active && tick >= 1) {
      const activePts: { x: number; y: number }[] = [];
      const endTick = tick;
      for (let i = 0; i <= endTick * RESOLUTION; i++) {
        const t = i / RESOLUTION;
        const m = Math.min(Math.pow(TICK_GROWTH, t), MAX_MULT);
        activePts.push({ x: getX(t), y: getY(m) });
      }

      const color = crashed ? "#ef4444" : "#00ff9d";
      const colorA = crashed ? "rgba(239,68,68," : "rgba(0,255,157,";

      // Fill under active curve
      const grad = ctx.createLinearGradient(0, pad.top, 0, H - pad.bottom);
      grad.addColorStop(0, colorA + "0.12)");
      grad.addColorStop(1, colorA + "0)");
      ctx.beginPath();
      ctx.moveTo(activePts[0].x, H - pad.bottom);
      for (const p of activePts) ctx.lineTo(p.x, p.y);
      ctx.lineTo(activePts[activePts.length - 1].x, H - pad.bottom);
      ctx.closePath();
      ctx.fillStyle = grad;
      ctx.fill();

      // Active line
      ctx.beginPath();
      ctx.moveTo(activePts[0].x, activePts[0].y);
      for (let i = 1; i < activePts.length; i++) ctx.lineTo(activePts[i].x, activePts[i].y);
      ctx.strokeStyle = color;
      ctx.lineWidth = 2.5;
      ctx.shadowColor = colorA + "0.5)";
      ctx.shadowBlur = 10;
      ctx.stroke();
      ctx.shadowBlur = 0;

      // End dot
      const last = activePts[activePts.length - 1];
      ctx.beginPath(); ctx.arc(last.x, last.y, 5, 0, Math.PI * 2); ctx.fillStyle = color; ctx.fill();
      ctx.beginPath(); ctx.arc(last.x, last.y, 9, 0, Math.PI * 2);
      ctx.strokeStyle = colorA + "0.3)"; ctx.lineWidth = 2; ctx.stroke();
    }
  }, [phase, tick, multiplier]);

  return <canvas ref={canvasRef} className="w-full h-full" style={{ display: "block" }} />;
}

// ── Bet row component (shared by My Bets & All Bets) ──

function BetRow({ e, showPlayer, showProfit, isNew }: { e: BetEntry; showPlayer?: boolean; showProfit?: boolean; isNew?: boolean }) {
  const actionStyle = e.action === "joined"
    ? "bg-yellow-400/15 text-yellow-400 border border-yellow-400/30"
    : e.action === "cashed"
    ? "bg-emerald-500/15 text-emerald-400 border border-emerald-500/30"
    : "bg-red-500/15 text-red-400 border border-red-500/30";
  const actionLabel = e.action === "joined" ? "JOINED" : e.action === "cashed" ? "CASHED" : "BUSTED";
  const multColor = e.action === "cashed" ? "text-emerald-400" : e.action === "busted" ? "text-red-400" : "text-zinc-600";

  const profit = e.action === "cashed" && e.payout ? (e.payout - e.stake) / 1e6
    : e.action === "busted" ? -(e.stake / 1e6)
    : null; // joined — no profit yet

  return (
    <tr className={`border-b border-zinc-800/50 hover:bg-purple-500/5 transition-colors ${isNew ? "animate-in" : ""}`}>
      {showPlayer && (
        <td className={`px-3 py-2 text-[10px] font-[family-name:var(--font-display)] ${e.mine ? "text-yellow-400 font-bold" : "text-purple-400"}`}>
          {e.mine ? "You" : `${e.bettor.slice(0, 6)}..${e.bettor.slice(-3)}`}
        </td>
      )}
      <td className="px-3 py-2 text-[12px] font-[family-name:var(--font-display)] text-zinc-400">
        #{e.round}
      </td>
      <td className={`px-3 py-2 text-[12px] font-bold font-[family-name:var(--font-display)] ${multColor}`}>
        {e.multBp ? `${(e.multBp / 10000).toFixed(2)}x` : "—"}
      </td>
      <td className="px-3 py-2 text-right text-[12px] text-white font-bold font-[family-name:var(--font-display)]">
        ${(e.stake / 1e6).toFixed(2)}
      </td>
      {showProfit && (
        <td className={`px-3 py-2 text-right text-[12px] font-bold font-[family-name:var(--font-display)] ${
          profit === null ? "text-zinc-600" : profit >= 0 ? "text-emerald-400" : "text-red-400"
        }`}>
          {profit === null ? "—" : profit >= 0 ? `+$${profit.toFixed(2)}` : `-$${Math.abs(profit).toFixed(2)}`}
        </td>
      )}
      <td className="px-3 py-2 text-right">
        <span className={`inline-block px-2 py-0.5 rounded-full text-[9px] font-bold tracking-wider font-[family-name:var(--font-display)] ${actionStyle}`}>
          {actionLabel}
        </span>
      </td>
    </tr>
  );
}

// ── Main ──

function CrashGame() {
  const searchParams = useSearchParams();
  const bankrollId = Number(searchParams.get("bankroll") || process.env.NEXT_PUBLIC_BANKROLL_ID || "1");
  const gameId = Number(searchParams.get("game") || "2");

  const { ready: walletReady, status: walletStatus, address, wallet, openModal } = useWallet();
  const [gameReady, setGameReady] = useState(false);
  const [gameStatus, setGameStatus] = useState(0); // 0=active, 1=paused, 2=killed

  // ── Config ──
  const [houseEdgeBp, setHouseEdgeBp] = useState(200);
  useEffect(() => {
    bff.games().then(gs => {
      const g = gs.find(g => g.id === gameId);
      if (g?.houseEdgeBp) setHouseEdgeBp(g.houseEdgeBp);
      if (g?.status) setGameStatus(g.status);
      setGameReady(true);
    }).catch(() => { setGameReady(true); });
  }, [gameId]);
  const houseEdgePct = (houseEdgeBp / 100).toFixed(1).replace(/\.0$/, "");

  // ── Game state ──
  const [phase, setPhase] = useState<CrashPhase>("waiting");
  const [roundId, setRoundId] = useState(0);
  const [multiplier, setMultiplier] = useState(1.0);
  const [crashPoint, setCrashPoint] = useState<number | null>(null);
  const [playerCount, setPlayerCount] = useState(0);
  const [aliveCount, setAliveCount] = useState(0);
  const [cashedCount, setCashedCount] = useState(0);
  const [blocksLeft, setBlocksLeft] = useState(0);
  const [currentTick, setCurrentTick] = useState(0);
  const [bustHistory, setBustHistory] = useState<number[]>([]);

  // ── Player state ──
  const [stakeInput, setStakeInput] = useState("1.00");
  const [joined, setJoined] = useState(false);
  const [betId, setBetId] = useState<number | null>(null);
  const [joining, setJoining] = useState(false);
  const [cashoutPhase, setCashoutPhase] = useState<null | "pending" | "won" | "busted">(null);
  const [cashoutMult, setCashoutMult] = useState<number | null>(null);
  const [cashoutPayout, setCashoutPayout] = useState<string | null>(null);

  // ── Wallet / balance ──
  const [balanceUusdc, setBalanceUusdc] = useState("0");
  const [bankrollBalance, setBankrollBalance] = useState("0");
  const [maxPayoutCapBps, setMaxPayoutCapBps] = useState(200);
  const [error, setError] = useState("");
  const [needsGrant, setNeedsGrant] = useState(false);
  const [granting, setGranting] = useState(false);
  const [needsFaucet, setNeedsFaucet] = useState(false);
  const [faucetLoading, setFaucetLoading] = useState(false);

  // ── Unified bet log (ref + counter pattern — immune to React batching) ──
  const betBuf = useRef<BetEntry[]>([] as BetEntry[]);
  const betLogRef = useRef<BetEntry[]>([] as BetEntry[]);
  const [, setBetTick] = useState(0);

  // ── Refs for SSE handler (avoid stale closures) ──
  const betIdRef = useRef<number | null>(null);
  const addressRef = useRef<string | null>(null);
  const cashoutPhaseRef = useRef<string | null>(null);
  const prevRoundRef = useRef(0);
  const joinClickTime = useRef(0);
  const cashoutClickTime = useRef(0);

  useEffect(() => { betIdRef.current = betId; }, [betId]);
  useEffect(() => { addressRef.current = address ?? null; }, [address]);
  useEffect(() => { cashoutPhaseRef.current = cashoutPhase; }, [cashoutPhase]);

  // ── Balance polling ──
  useEffect(() => {
    if (!address) return;
    const f = () => bff.balance(address).then(b => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
    f();
    const id = setInterval(f, 5000);
    return () => clearInterval(id);
  }, [address]);

  // ── Bankroll info ──
  useEffect(() => {
    fetch("/api/chain/house/types/bankrolls")
      .then(r => r.json())
      .then(data => {
        const br = data.views?.find((v: any) => v.bankroll?.id === String(bankrollId));
        if (br) {
          setBankrollBalance(br.balance);
          if (br.bankroll?.max_payout_cap_bps) setMaxPayoutCapBps(br.bankroll.max_payout_cap_bps);
        }
      })
      .catch(() => {});
  }, [bankrollId]);

  // ═══════════════════════════════════════════════════════════════
  //  SINGLE SSE CONNECTION — processes all crash events
  //
  //  calcEvent topics from crash WASM:
  //    "state"   → {phase, round, mult_bp, tick, blocks_left, players, active, cashed, stake}
  //    "joined"  → {bet_id, addr, stake, players}
  //    "cashout" → {bet_id, addr, mult_bp, payout}
  //    "settled" → {bet_id, addr, payout, kind}  (kind: 1=win, 2=loss)
  // ═══════════════════════════════════════════════════════════════
  useEffect(() => {
    const bffUrl = process.env.NEXT_PUBLIC_BFF_DIRECT_URL || `${window.location.protocol}//${window.location.hostname}:3100`;
    const url = `${bffUrl}/stream?games=${gameId}`;
    const es = new EventSource(url);
    let lastCrashMultBp = 0;
    let localPhase: CrashPhase = "waiting"; // tracks phase inside the handler — no React delay
    let replaying = false;
    betBuf.current = [];
    const buf: BetEntry[] = betBuf.current;

    es.onmessage = (ev) => {
      let raw: any;
      try { raw = JSON.parse(ev.data); } catch { return; }
      // Replay phase handling
      if (raw.connected && raw.replay === true) { replaying = true; return; }
      if (raw.replay === false) {
        replaying = false;
        betLogRef.current = buf.slice(0, 500);
        setBetTick(t => t + 1);
        return;
      }
      if (raw.heartbeat) return;

      const events = raw.calcEvents;
      if (!events || !Array.isArray(events)) return;

      for (const ce of events) {
        if (ce.calculatorId !== gameId) continue;
        let d: any;
        try { d = JSON.parse(ce.data); } catch { continue; }

        switch (ce.topic) {

          // ── Round state (every block during open/tick/crashed) ──
          case "state": {
            const r = d.round || 0;

            // New round → reset player state
            if (r !== prevRoundRef.current && prevRoundRef.current > 0) {
              setJoined(false);
              setBetId(null);
              setCashoutPhase(null);
              setCashoutMult(null);
              setCashoutPayout(null);
              setError("");
            }
            prevRoundRef.current = r;
            setRoundId(r);
            setPlayerCount(d.players || 0);
            setAliveCount(d.active ?? d.players ?? 0);
            setCashedCount(d.cashed || 0);
            setBlocksLeft(d.blocks_left || 0);
            setCurrentTick(d.tick || 0);

            if (d.phase === "open") {
              localPhase = "open";
              setPhase("open");
              setMultiplier(1.0);
              setCrashPoint(null);
            } else if (d.phase === "tick") {
              localPhase = "tick";
              setPhase("tick");
              setMultiplier((d.mult_bp || 10000) / 10000);
            } else if (d.phase === "crashed") {
              const cpBp = d.mult_bp || 10000;
              lastCrashMultBp = cpBp;
              if (localPhase !== "crashed") {
                setCrashPoint(cpBp / 10000);
                setBustHistory(prev => [cpBp / 10000, ...prev].slice(0, 20));
                // If player is in round and hasn't cashed out, they're busted
                if (cashoutPhaseRef.current && cashoutPhaseRef.current !== "won") setCashoutPhase("busted");
              }
              localPhase = "crashed";
              setPhase("crashed");
              setMultiplier(cpBp / 10000);
            }
            break;
          }

          // ── Player joined the round ──
          case "joined": {
            const isMine = d.bet_id === betIdRef.current || d.addr === addressRef.current;
            buf.unshift({
              betId: d.bet_id, bettor: d.addr, round: prevRoundRef.current,
              stake: Number(d.stake) || 0, action: "joined" as const, mine: isMine,
            });
            // Set betId from SSE when our join is confirmed on-chain
            if (isMine && d.bet_id && !betIdRef.current) {
              setBetId(d.bet_id);
            }
            if (!replaying) {
              if (buf.length > 500) buf.length = 500;
              betLogRef.current = buf.slice(0, 500);
              setBetTick(t => t + 1);
            }
            break;
          }

          // ── Player cashed out (this IS the settlement for winners) ──
          case "cashout": {
            const isMine = d.bet_id === betIdRef.current || d.addr === addressRef.current;
            buf.unshift({
              betId: d.bet_id, bettor: d.addr, round: d.round || prevRoundRef.current,
              stake: Number(d.stake) || 0, action: "cashed" as const,
              multBp: d.mult_bp, payout: Number(d.payout) || 0, mine: isMine,
            });
            if (!replaying) {
              if (buf.length > 500) buf.length = 500;
              betLogRef.current = buf.slice(0, 500);
              setBetTick(t => t + 1);
            }

            if (isMine) {
              if (cashoutClickTime.current) console.log(`[crash] click → cashout: ${(performance.now() - cashoutClickTime.current).toFixed(0)}ms`);
              const mult = (d.mult_bp || 10000) / 10000;
              setCashoutPhase("won");
              setCashoutMult(mult);
              setCashoutPayout(String(Number(d.payout) || 0));
              const addr = addressRef.current;
              if (addr) bff.balance(addr).then(b => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
            }
            break;
          }

          // ── Player settled (CASHED if win, BUSTED if loss) ──
          case "settled": {
            const isWin = d.kind === 1;
            const isMine = d.bet_id === betIdRef.current || d.addr === addressRef.current;
            buf.unshift({
              betId: d.bet_id, bettor: d.addr, round: d.round || prevRoundRef.current,
              stake: Number(d.stake) || 0, action: isWin ? "cashed" as const : "busted" as const,
              multBp: d.mult_bp || lastCrashMultBp, payout: Number(d.payout) || 0, mine: isMine,
            });
            if (!replaying) {
              if (buf.length > 500) buf.length = 500;
              betLogRef.current = buf.slice(0, 500);
              setBetTick(t => t + 1);
            }

            if (isMine && !isWin) {
              if (joinClickTime.current) console.log(`[crash] join → busted: ${(performance.now() - joinClickTime.current).toFixed(0)}ms`);
              setCashoutPhase("busted");
            }
            break;
          }
        }
      }
    };

    return () => es.close();
  }, [gameId]);

  // ── Actions ──

  const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
  const canJoin = phase === "open" && !joined && !joining;
  const isCrashed = phase === "crashed";

  const handleJoin = useCallback(async () => {
    if (walletStatus !== "unlocked" || !wallet || !address) { openModal(); return; }
    if (phase !== "open") { setError("Wait for the next round"); return; }
    const stake = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
    if (stake < 10000) { setError("Minimum stake is $0.01"); return; }
    if (stake > Number(balanceUusdc)) { setError("Insufficient balance"); return; }

    setError(""); setJoining(true); setNeedsGrant(false); setNeedsFaucet(false);
    joinClickTime.current = performance.now();

    try {
      await bff.relayPlaceBet({ address, bankrollId, gameId, stake: stake.toString(), gameState: [] });
      console.log(`[crash] click → joined: ${(performance.now() - joinClickTime.current).toFixed(0)}ms`);
      if (address) markBetPlaced(address);
      // betId will be set from SSE "joined" event matching our address
      setJoined(true);
      if (address) bff.balance(address).then(b => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
    } catch (e: any) {
      const msg = e.message || "Join failed";
      if (msg.includes("authorization not found") || msg.includes("unauthorized")) {
        setNeedsGrant(true); setError("Instant betting not authorized. Click 'Authorize' below.");
      } else if (/insufficient funds|smaller than|account.*not found|not on chain|spendable balance/i.test(msg)) {
        setNeedsFaucet(true); setError(humanizeError(msg));
      } else setError(humanizeError(msg));
    }
    setJoining(false);
  }, [walletStatus, address, wallet, phase, stakeInput, balanceUusdc, bankrollId, gameId, openModal]);

  const handleCashout = useCallback(async () => {
    if (!address || !betId || cashoutPhase) return;
    setCashoutPhase("pending"); setCashoutMult(multiplier); setError("");
    cashoutClickTime.current = performance.now();

    try {
      await bff.relayGameAction({ address, betId, action: [1] });
    } catch (e: any) {
      setCashoutPhase(null); setCashoutMult(null);
      const msg = e.message || "Cashout failed";
      setError(humanizeError(msg));
    }
  }, [address, betId, cashoutPhase, multiplier]);

  // ═══════════════════════════════════════════════════════════════
  //  RENDER
  // ═══════════════════════════════════════════════════════════════

  if (gameStatus === 2) return <PagePreloader message="Crash is temporarily unavailable. The game calculator was stopped by the system." />;
  if (gameStatus === 1) return <PagePreloader message="Crash is currently paused by the bankroll operator." />;
  if (!walletReady || !gameReady) return <PagePreloader />;

  return (
    <div className="min-h-screen flex flex-col relative page-bg">
      <Header balance={balanceUusdc} />
      <ExoScanBar />

      <main className="flex-1 relative z-10 pt-[56px]">
        <div className="max-w-[960px] mx-auto px-4 sm:px-6">

          {/* ── Title ── */}
          <section className="pt-6 pb-4 text-center">
            <h1 className="text-6xl font-black text-white leading-none mb-2 font-[family-name:var(--font-title)] tracking-wider neon-gold">CRASH</h1>
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

          {/* ── Bust history ── */}
          {bustHistory.length > 0 && (
            <div className="flex flex-wrap gap-1.5 mb-4 justify-center">
              {bustHistory.slice(0, 15).map((b, i) => (
                <span key={i} className={`px-2.5 py-1 rounded-full text-[11px] font-black font-[family-name:var(--font-title)] tracking-wider ${
                  b >= 2 ? "bg-emerald-500/15 text-emerald-400 border border-emerald-500/30" : "bg-red-500/15 text-red-400 border border-red-500/30"
                }`}>{b.toFixed(2)}x</span>
              ))}
            </div>
          )}

          {/* ── Game area ── */}
          <section className="pb-8">
            <div className="flex flex-col lg:flex-row gap-6">

              {/* LEFT: Chart */}
              <div className="flex-1 min-w-0">
                <div className={`glass-card rounded-3xl overflow-hidden transition-all duration-300 ${
                  isCrashed ? "border-red-500/40 shadow-[0_0_40px_rgba(239,68,68,0.15)]"
                    : phase === "tick" ? "border-emerald-500/30 shadow-[0_0_40px_rgba(0,255,157,0.1)]" : ""
                }`}>
                  <div className="px-5 py-3 border-b border-purple-500/30 flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <div className={`w-2 h-2 rounded-full ${
                        isCrashed ? "bg-red-500" : phase === "tick" ? "bg-emerald-400 live-dot" : phase === "open" ? "bg-yellow-400 live-dot" : "bg-zinc-600"
                      }`} />
                      <span className="text-[11px] tracking-[0.25em] text-purple-300 uppercase font-bold font-[family-name:var(--font-display)]">
                        {phase === "open" ? "JOINING" : phase === "tick" ? "LIVE" : isCrashed ? "CRASHED" : "WAITING"}
                      </span>
                    </div>
                    <span className="text-[11px] text-zinc-400 font-[family-name:var(--font-display)]">
                      Round #{roundId} · {playerCount} joined{phase === "tick" && aliveCount < playerCount ? ` · ${aliveCount} alive` : ""}
                    </span>
                  </div>

                  <div className="relative h-[280px] sm:h-[340px]">
                    <CrashChart phase={phase} tick={currentTick} multiplier={multiplier} />
                    <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
                      {phase === "waiting" && (
                        <>
                          <div className="text-[5rem] font-black font-[family-name:var(--font-title)] text-zinc-800 leading-none">?</div>
                          <div className="text-lg text-zinc-600 font-[family-name:var(--font-title)] tracking-widest mt-2">WAITING</div>
                        </>
                      )}
                      {phase === "open" && (
                        <>
                          <div className="text-[3rem] font-black font-[family-name:var(--font-title)] text-yellow-400 leading-none countdown-tick" style={{ textShadow: '0 0 30px rgba(234,179,8,0.4)' }}>{blocksLeft}</div>
                          <div className="text-sm text-yellow-300/70 font-[family-name:var(--font-display)] tracking-wider mt-1">BLOCKS UNTIL LAUNCH</div>
                        </>
                      )}
                      {phase === "tick" && (
                        <div className="text-[4.5rem] sm:text-[5.5rem] font-black font-[family-name:var(--font-title)] text-[#00ff9d] leading-none" style={{ textShadow: '0 0 40px rgba(0,255,157,0.4)' }}>
                          {multiplier.toFixed(2)}x
                        </div>
                      )}
                      {isCrashed && (
                        <>
                          <div className="text-[4.5rem] sm:text-[5.5rem] font-black font-[family-name:var(--font-title)] text-red-400 leading-none" style={{ textShadow: '0 0 40px rgba(239,68,68,0.4)' }}>
                            {(crashPoint || multiplier).toFixed(2)}x
                          </div>
                          <div className="text-lg text-red-400/70 font-[family-name:var(--font-title)] tracking-[0.2em] mt-1">CRASHED</div>
                        </>
                      )}
                    </div>
                  </div>

                  {/* Player result bar */}
                  {joined && (
                    <div className={`px-5 py-3 border-t border-purple-500/30 flex items-center justify-between ${
                      cashoutPhase === "won" ? "bg-emerald-500/5" : cashoutPhase === "busted" || (isCrashed && !cashoutPhase) ? "bg-red-500/5" : ""
                    }`}>
                      <div className="flex items-center gap-2">
                        <Rocket className={`w-4 h-4 ${
                          cashoutPhase === "won" ? "text-emerald-400" : cashoutPhase === "busted" || (isCrashed && !cashoutPhase) ? "text-red-400" : "text-yellow-400"
                        }`} />
                        <span className={`text-sm font-bold font-[family-name:var(--font-display)] ${
                          cashoutPhase === "won" ? "text-emerald-400" : cashoutPhase === "busted" || (isCrashed && !cashoutPhase) ? "text-red-400" : cashoutPhase === "pending" ? "text-yellow-400" : "text-white"
                        }`}>
                          {cashoutPhase === "won" ? `Cashed out at ${cashoutMult?.toFixed(2) || "?"}x`
                            : cashoutPhase === "busted" || (isCrashed && !cashoutPhase) ? "Busted!"
                            : cashoutPhase === "pending" ? "Cashout pending..."
                            : `In round · $${(stakeUusdc / 1e6).toFixed(2)}`}
                        </span>
                      </div>
                      {cashoutPhase === "won" && cashoutPayout && (
                        <span className="text-sm font-bold font-[family-name:var(--font-display)] text-emerald-400">+${((Number(cashoutPayout) - stakeUusdc) / 1e6).toFixed(2)}</span>
                      )}
                      {(cashoutPhase === "busted" || (isCrashed && !cashoutPhase && joined)) && (
                        <span className="text-sm font-bold font-[family-name:var(--font-display)] text-red-400">-${(stakeUusdc / 1e6).toFixed(2)}</span>
                      )}
                    </div>
                  )}
                </div>
              </div>

              {/* RIGHT: Controls */}
              <div className="w-full lg:w-[340px] shrink-0 glass-panel rounded-3xl p-5 sm:p-6 flex flex-col gap-4">
                {/* Stats */}
                <div className="grid grid-cols-2 gap-2">
                  <div className="glass-panel rounded-2xl p-3 text-center">
                    <div className="text-[9px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">JOINED</div>
                    <div className={`text-lg font-black font-[family-name:var(--font-title)] ${playerCount > 0 ? "text-yellow-400" : "text-zinc-600"}`}>{playerCount}</div>
                  </div>
                  <div className="glass-panel rounded-2xl p-3 text-center">
                    <div className="text-[9px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">CASHED</div>
                    <div className={`text-lg font-black font-[family-name:var(--font-title)] ${cashedCount > 0 ? "text-emerald-400" : "text-zinc-600"}`}>
                      {cashedCount}
                    </div>
                  </div>
                </div>

                {/* Stake */}
                <div className="glass-panel rounded-3xl p-5">
                  <div className="text-[10px] tracking-[0.25em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-2">STAKE</div>
                  <div className="grid grid-cols-5 gap-1.5">
                    {["0.50", "1.00", "2.00", "5.00", "10.00"].map(v => (
                      <button key={v} onClick={() => setStakeInput(v)} disabled={!canJoin}
                        className={`py-2.5 rounded-2xl text-xs font-bold transition-all cursor-pointer font-[family-name:var(--font-display)] ${
                          stakeInput === v ? "stake-chip-active" : "bg-zinc-800 border border-yellow-400/30 text-zinc-400 hover:text-white hover:border-yellow-400/50"
                        } disabled:opacity-50 disabled:cursor-not-allowed`}>
                        ${v.replace(/\.00$/, "")}
                      </button>
                    ))}
                  </div>
                </div>

                {error && (
                  <div className="bg-red-500/10 border border-red-500/20 rounded-2xl px-4 py-2.5">
                    <p className="text-xs text-red-400 text-center font-[family-name:var(--font-display)]">{error}</p>
                  </div>
                )}

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

                {needsGrant && wallet && address && (
                  <button onClick={async () => {
                      setGranting(true); setError("");
                      try { const ri = await bff.relayInfo();
                        if (ri.enabled) { const { grantRelay } = await import("@/lib/signer");
                          const r = await grantRelay(wallet, address, ri.relayAddress);
                          if (r.code === 0) { setNeedsGrant(false); setError(""); } else setError("Authorization failed.");
                        }
                      } catch (e: any) { setError(e.message?.slice(0, 100) || "Authorization failed"); } setGranting(false);
                    }} disabled={granting}
                    className="w-full py-3 border border-yellow-400/30 bg-yellow-400/10 text-yellow-400 rounded-2xl text-sm font-bold hover:bg-yellow-400/20 transition-colors disabled:opacity-50 cursor-pointer flex items-center justify-center gap-2 font-[family-name:var(--font-display)]">
                    {granting ? "Authorizing..." : "Authorize Instant Betting"}
                  </button>
                )}

                {/* CTA */}
                {(() => {
                  if (joined && (cashoutPhase === "busted" || (isCrashed && !cashoutPhase))) return (
                    <div className="space-y-2">
                      <div className="text-center py-3 rounded-2xl border border-red-500/30 bg-red-500/5">
                        <div className="text-xl font-black font-[family-name:var(--font-title)] tracking-wider text-red-400">BUSTED!</div>
                        <div className="text-sm font-[family-name:var(--font-display)] text-red-400/70">-${(stakeUusdc / 1e6).toFixed(2)}</div>
                      </div>
                      {betId && <a href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bets/${gameId}/${betId}`} target="_blank" rel="noreferrer" className="block text-center text-[11px] text-purple-400 hover:text-yellow-400 transition-colors font-[family-name:var(--font-display)]"><Shield className="w-3 h-3 inline mr-1" />Verify on ExoScan</a>}
                    </div>
                  );
                  if (joined && cashoutPhase === "won") return (
                    <div className="space-y-2">
                      <div className="text-center py-3 rounded-2xl border border-emerald-500/30 bg-emerald-500/5">
                        <div className="text-xl font-black font-[family-name:var(--font-title)] tracking-wider text-emerald-400">CASHED OUT!</div>
                        <div className="text-sm font-[family-name:var(--font-display)] text-emerald-400/70">+${((Number(cashoutPayout || 0) - stakeUusdc) / 1e6).toFixed(2)} profit at {cashoutMult?.toFixed(2)}x</div>
                      </div>
                      {betId && <a href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bets/${gameId}/${betId}`} target="_blank" rel="noreferrer" className="block text-center text-[11px] text-purple-400 hover:text-yellow-400 transition-colors font-[family-name:var(--font-display)]"><Shield className="w-3 h-3 inline mr-1" />Verify on ExoScan</a>}
                    </div>
                  );
                  if (phase === "tick" && joined && cashoutPhase === "pending") return (
                    <button disabled className="w-full btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-purple-300 font-bold flex items-center justify-center gap-2 tracking-widest">
                      <Loader2 className="w-5 h-5 animate-spin" /> CASHING OUT...
                    </button>
                  );
                  if (phase === "tick" && joined && !cashoutPhase) return (
                    <button onClick={handleCashout} className="w-full btn-action rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-black font-bold tracking-widest cursor-pointer flex flex-col items-center justify-center">
                      <span>CASH OUT ${formatUSDC(Math.floor(stakeUusdc * multiplier).toString())} USDC</span>
                      <span className="text-xs font-normal opacity-80">(+${((stakeUusdc * multiplier - stakeUusdc) / 1e6).toFixed(2)} profit at {multiplier.toFixed(2)}x)</span>
                    </button>
                  );
                  if (phase === "open" && joined) return (
                    <button disabled className="w-full btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-yellow-400 font-bold flex items-center justify-center gap-2 tracking-widest">
                      <Rocket className="w-5 h-5" /> YOU'RE IN — ${(stakeUusdc / 1e6).toFixed(2)}
                    </button>
                  );
                  if (joining) return (
                    <button disabled className="w-full btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-purple-300 font-bold flex items-center justify-center gap-2 tracking-widest">
                      <Loader2 className="w-5 h-5 animate-spin" /> JOINING...
                    </button>
                  );
                  if (canJoin) return (
                    <button onClick={handleJoin} className="w-full btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest cursor-pointer flex items-center justify-center gap-2">
                      {walletStatus === "none" ? "CREATE A WALLET" : walletStatus === "locked" ? "UNLOCK YOUR WALLET" : <><Rocket className="w-5 h-5" /> JOIN ROUND — ${(stakeUusdc / 1e6).toFixed(2)}</>}
                    </button>
                  );
                  return (
                    <div className="w-full btn-muted rounded-2xl py-4 text-center text-lg font-[family-name:var(--font-title)] font-bold tracking-wider uppercase">
                      {isCrashed ? "NEXT ROUND SOON..." : "ROUND IN PROGRESS..."}
                    </div>
                  );
                })()}
              </div>
            </div>
          </section>

          {/* ── Tables: My Bets | All Bets ── */}
          <section className="pb-6">
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">

              {/* MY BETS */}
              <div className="glass-panel rounded-3xl overflow-hidden">
                <div className="px-5 py-3 border-b border-purple-500/30 flex items-center gap-2.5">
                  <div className="w-2 h-2 rounded-full bg-yellow-400 live-dot" />
                  <h3 className="text-[11px] tracking-[0.25em] text-purple-300 uppercase font-bold font-[family-name:var(--font-display)]">My Bets</h3>
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
                        <th className="text-left px-3 py-2 font-bold">Round</th>
                        <th className="text-left px-3 py-2 font-bold">Mult</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Profit</th>
                        <th className="text-right px-3 py-2 font-bold">Action</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const my = betLogRef.current.filter(e => e.mine);
                        if (my.length === 0) return <tr><td colSpan={5} className="text-center py-6 text-zinc-500">No bets yet</td></tr>;
                        return my.slice(0, 500).map((e, i) => <BetRow key={`${e.betId}-${e.action}`} e={e} showProfit isNew={i === 0} />);
                      })()}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* ALL BETS */}
              <div className="glass-panel rounded-3xl overflow-hidden">
                <div className="px-5 py-3 border-b border-purple-500/30 flex items-center gap-2.5">
                  <div className="w-2 h-2 rounded-full bg-emerald-400 live-dot" />
                  <h3 className="text-[11px] tracking-[0.25em] text-purple-300 uppercase font-bold font-[family-name:var(--font-display)]">All Bets</h3>
                  <span className="text-[10px] text-zinc-400 font-mono ml-auto">Live Feed</span>
                </div>
                <div className="max-h-[320px] overflow-y-auto">
                  <table className="w-full text-[12px]">
                    <thead>
                      <tr className="text-[10px] tracking-[0.15em] text-purple-300 uppercase font-[family-name:var(--font-title)] border-b border-purple-500/30">
                        <th className="text-left px-3 py-2 font-bold">Player</th>
                        <th className="text-left px-3 py-2 font-bold">Round</th>
                        <th className="text-left px-3 py-2 font-bold">Mult</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Action</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const others = betLogRef.current.filter(e => !e.mine);
                        if (others.length === 0) return <tr><td colSpan={5} className="text-center py-6 text-zinc-500">Waiting for bets...</td></tr>;
                        return others.slice(0, 500).map((e, i) => <BetRow key={`${e.betId}-${e.action}`} e={e} showPlayer isNew={i === 0} />);
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

export default function CrashPage() {
  return (
    <Suspense fallback={<div className="min-h-screen flex items-center justify-center text-[#8a8070]">Loading...</div>}>
      <CrashGame />
    </Suspense>
  );
}
