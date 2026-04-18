"use client";

import { useState, useEffect, useCallback, useRef, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { Loader2 } from "lucide-react";
import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import { ExoScanBar, markBetPlaced } from "@/components/ExoScanBar";
import { PagePreloader } from "@/components/PagePreloader";
import { useWallet } from "@/contexts/WalletContext";
import { useStream } from "@/lib/useStream";
import { useBetFeed } from "@/lib/useBetFeed";
import { bff, formatUSDC, getUSDCBalance, type StatusResponse } from "@/lib/bff";
import { ensureRelayGrant } from "@/lib/signer";

function DiceGame() {
  const searchParams = useSearchParams();
  const bankrollId = Number(searchParams.get("bankroll") || process.env.NEXT_PUBLIC_BANKROLL_ID || "1");
  const gameId = Number(searchParams.get("game") || "1");

  // Fetch game info from BFF
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
  const minChancePct = 1;
  const maxChancePct = 95;

  const { ready: walletReady, status: walletStatus, address, wallet, openModal } = useWallet();
  const stream = useStream(gameId);

  // Game state
  const [mode, setMode] = useState<"over" | "under">("over");
  const [threshold, setThreshold] = useState(50); // percent
  const [stakeInput, setStakeInput] = useState("1.00");

  // Preview
  const [preview, setPreview] = useState<{
    chancePct: number;
    multiplier: number;
    payoutUusdc: number;
    profitUusdc: number;
  } | null>(null);

  // Balance
  const [balanceUusdc, setBalanceUusdc] = useState("0");
  const [bankrollBalance, setBankrollBalance] = useState("0");
  const [maxPayoutCapBps, setMaxPayoutCapBps] = useState(200);
  const [bankrollInfo, setBankrollInfo] = useState<{
    name: string; creator: string; isPrivate: boolean; available: string;
  } | null>(null);

  // Track recent dice settlements from SSE — useBetFeed (ref + counter, no batching issues)
  type DiceBet = {
    betId: number;
    bettor: string;
    stake: string;
    payout: string;
    win: boolean;
    roll?: number;       // 0-10000 basis points
    chanceBp?: number;
    multBp?: number;
    ts: number;
  };
  const recentBets = useBetFeed<DiceBet>(gameId, ["settle"], (_ce, d) => ({
    betId: d.entry_id,
    bettor: d.addr || "",
    stake: String(d.stake || "0"),
    payout: String(d.payout || "0"),
    win: d.result === "win",
    roll: d.roll,
    chanceBp: d.chance_bp,
    multBp: d.mult_bp,
    ts: Date.now(),
  }));

  // Fetch bankroll balance from chain
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

  // Bet state
  const [betting, setBetting] = useState(false);
  const [pendingBetAddr, setPendingBetAddr] = useState<string | null>(null);
  const [lastBetId, setLastBetId] = useState<number | null>(null);
  const [result, setResult] = useState<any>(null);
  const [hasPlayed, setHasPlayed] = useState(false);
  const [error, setError] = useState("");
  const [needsGrant, setNeedsGrant] = useState(false);
  const [granting, setGranting] = useState(false);
  const [needsFaucet, setNeedsFaucet] = useState(false);
  const [faucetLoading, setFaucetLoading] = useState(false);
  const betStartTime = useRef<number>(0);

  // SSE settlement — watch directly for this bet's settlement
  useEffect(() => {
    if (!pendingBetAddr || !stream.lastEvent) return;
    // Match settlement by address + gameId (dice settles in same block as placement)
    const settlement = stream.lastEvent.settlements?.find((s: any) => s.bettor === pendingBetAddr && s.gameId === gameId);
    if (!settlement) return;

    // Extract engine result (roll, chance_bp, mult_bp) from calc_event
    let engineResult: any = null;
    for (const ce of stream.lastEvent.calcEvents || []) {
      if (ce.calculatorId !== gameId || ce.topic !== "settle") continue;
      try {
        const d = JSON.parse(ce.data);
        if (d.addr === pendingBetAddr) { engineResult = d; break; }
      } catch {}
    }

    const pendingBetId = settlement.betId;
    const payout = Number(settlement.payout || settlement.payoutAmount || 0);
    const elapsed = betStartTime.current ? (performance.now() - betStartTime.current).toFixed(0) : "?";
    console.log(`[dice] click → result: ${elapsed}ms`);

    setResult({
      id: pendingBetId,
      bankrollId,
      gameId,
      engine: "",
      bettor: settlement.bettor,
      stake: { denom: "uusdc", amount: String(settlement.netStake || 0) },
      phase: "GAME_PHASE_DONE",
      result: {
        win: payout > 0,
        payout: String(payout) + "uusdc",
        ...(engineResult || {}),
      },
    });
    setLastBetId(pendingBetId);
    setBetting(false);
    setPendingBetAddr(null);
    if (address) bff.balance(address).then((b) => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
  }, [stream.lastEvent?.height, pendingBetAddr, address, bankrollId, gameId]);

  // Fetch balance
  useEffect(() => {
    if (!address) return;
    const fetch = () => bff.balance(address).then((b) => setBalanceUusdc(getUSDCBalance(b.balances))).catch(() => {});
    fetch();
    const id = setInterval(fetch, 5000);
    return () => clearInterval(id);
  }, [address]);

  // Calculate preview client-side
  useEffect(() => {
    const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
    if (stakeUusdc < 10000) { setPreview(null); return; }

    const chancePct = mode === "over" ? 100 - threshold : threshold;
    const multiplier = chancePct > 0 ? 100 / chancePct : 0;
    const payoutUusdc = Math.floor(stakeUusdc * multiplier);
    const profitUusdc = payoutUusdc - stakeUusdc;

    setPreview({ chancePct, multiplier, payoutUusdc, profitUusdc });
  }, [mode, threshold, stakeInput, houseEdgeBp]);

  const handleBet = useCallback(async () => {
    if (walletStatus !== "unlocked" || !wallet || !address) {
      openModal();
      return;
    }

    const stakeUusdc = Math.floor(parseFloat(stakeInput || "0") * 1_000_000);
    if (stakeUusdc < 10000) {
      setError("Minimum stake is $0.01");
      return;
    }
    if (stakeUusdc > Number(balanceUusdc)) {
      setError("Insufficient balance");
      return;
    }

    setError("");
    setNeedsGrant(false);
    setNeedsFaucet(false);
    setHasPlayed(true);
    setBetting(true);
    betStartTime.current = performance.now();

    try {
      const thresholdBp = threshold * 100;
      // WASM dice expects binary: mode(1 byte) + threshold(8 bytes LE)
      // mode: 1=over, 2=under
      const modeByte = mode === "over" ? 1 : 2;
      const buf = new ArrayBuffer(9);
      const view = new DataView(buf);
      view.setUint8(0, modeByte);
      // Little-endian uint64
      view.setUint32(1, thresholdBp, true);
      view.setUint32(5, 0, true);
      const gameState = Array.from(new Uint8Array(buf));

      const result = await bff.relayPlaceBet({
        address,
        bankrollId,
        gameId,
        stake: stakeUusdc.toString(),
        gameState,
      });

      if (address) markBetPlaced(address);
      // Bet ID will be resolved from SSE settlement matching by address
      setPendingBetAddr(address);
      // betting stays true — useEffect on settledBet will clear it
    } catch (e: any) {
      const msg = e.message || "Bet failed";
      if (msg.includes("authorization not found") || msg.includes("unauthorized")) {
        setNeedsGrant(true);
        setError("Instant betting not authorized. Click 'Authorize' below.");
      } else if (/insufficient funds|smaller than|account.*not found|not on chain|spendable balance/i.test(msg)) {
        setNeedsFaucet(true);
        setError("Insufficient balance. Get test USDC below.");
      } else if (/status=10/i.test(msg) || /rejected bet/i.test(msg)) {
        setError("Bet rejected by the game. Try again.");
      } else if (/exceeds max cap|entry risk|solvency/i.test(msg)) {
        setError("Bankroll can't cover this bet. Lower your stake.");
      } else if (/beacon.*unavailable/i.test(msg)) {
        setError("Games temporarily paused");
      } else {
        setError(msg.replace(/failed to execute message; message index: \d+: /g, "").replace(/tx [A-F0-9]+: tx failed code=\d+: /g, "").slice(0, 200));
      }
      setBetting(false);
    }
  }, [walletStatus, address, stakeInput, balanceUusdc, threshold, mode, bankrollId, gameId, openModal]);

  const winChance = mode === "over" ? 100 - threshold : threshold;
  const isWin = result?.result?.win === true;


  if (gameStatus === 2) return <PagePreloader message="Dice is temporarily unavailable. The game calculator was stopped by the system." />;
  if (gameStatus === 1) return <PagePreloader message="Dice is currently paused by the bankroll operator." />;
  if (!walletReady || !gameReady) return <PagePreloader />;

  return (
    <div className="min-h-screen flex flex-col relative page-bg">

      <Header balance={balanceUusdc} />
      <ExoScanBar />

      {/* ── MAIN ── */}
      <main className="flex-1 relative z-10 pt-[56px]">
        <div className="max-w-[960px] mx-auto px-4 sm:px-6">

          {/* ── Breadcrumb + heading ── */}
          <section className="pt-6 pb-4 text-center">
            <h1 className="text-6xl font-black text-white leading-none mb-2 font-[family-name:var(--font-title)] tracking-wider neon-gold">
              DICE
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

          {/* ── Game area: single column mobile, two column desktop ── */}
          <section className="pb-8">
            <div className="flex flex-col lg:flex-row gap-6">

              {/* ── LEFT: Roll result (desktop only) + mode selector ── */}
              <div className="hidden lg:flex flex-1 min-w-0 flex-col items-center justify-center">
                <div className="glass-card rounded-3xl p-8 w-full max-w-sm flex flex-col items-center justify-center min-h-[320px]">
                  {betting ? (
                    <div className="flex flex-col items-center gap-3">
                      <Loader2 className="w-12 h-12 animate-spin text-purple-400/50" />
                      <div className="text-lg text-zinc-400 font-[family-name:var(--font-title)] tracking-wider">ROLLING...</div>
                    </div>
                  ) : result && result.result ? (
                    <div className="text-center">
                      <div className="text-[5rem] font-black font-[family-name:var(--font-title)] leading-none" style={{
                        color: isWin ? '#00ff9d' : '#f87171',
                        textShadow: isWin ? '0 0 40px rgba(0,255,157,0.5)' : '0 0 40px rgba(248,113,113,0.5)'
                      }}>
                        {result.result.roll != null ? (result.result.roll / 100).toFixed(2) : "—"}
                      </div>
                      <div className={`text-xl font-black font-[family-name:var(--font-title)] tracking-[0.15em] mt-2 ${isWin ? "text-emerald-400" : "text-red-400"}`}>
                        {isWin ? "WIN" : "LOSS"}
                      </div>
                      <div className={`text-lg font-bold font-[family-name:var(--font-display)] mt-1 ${isWin ? "text-emerald-400/70" : "text-red-400/70"}`}>
                        {isWin
                          ? `+$${((parseInt(String(result.result?.payout || "0").replace("uusdc", "")) - parseInt(result.stake?.amount || "0")) / 1e6).toFixed(2)}`
                          : `-$${(parseInt(result.stake?.amount || "0") / 1e6).toFixed(2)}`}
                      </div>
                    </div>
                  ) : (
                    <div className="text-center">
                      <div className="text-[8rem] font-black font-[family-name:var(--font-title)] leading-none text-zinc-800">
                        ?
                      </div>
                      <div className="text-xl text-zinc-600 font-[family-name:var(--font-title)] mt-3 tracking-widest">
                        ROLL TO PLAY
                      </div>
                    </div>
                  )}
                </div>

                {/* Mode selector (desktop) */}
                <div className="glass-panel rounded-2xl mt-4 flex gap-2 w-full max-w-sm p-2">
                  <button onClick={() => setMode("over")}
                    className={`flex-1 py-2.5 rounded-xl text-xs font-bold tracking-wider transition-all font-[family-name:var(--font-display)] cursor-pointer ${
                      mode === "over" ? "bg-purple-500/15 text-yellow-400 border border-yellow-400/30"
                        : "text-zinc-500 border border-purple-500/15 hover:text-white hover:border-purple-500/30"
                    }`}>ROLL OVER</button>
                  <button onClick={() => setMode("under")}
                    className={`flex-1 py-2.5 rounded-xl text-xs font-bold tracking-wider transition-all font-[family-name:var(--font-display)] cursor-pointer ${
                      mode === "under" ? "bg-purple-500/15 text-yellow-400 border border-yellow-400/30"
                        : "text-zinc-500 border border-purple-500/15 hover:text-white hover:border-purple-500/30"
                    }`}>ROLL UNDER</button>
                </div>
              </div>

              {/* ── RIGHT / MOBILE: Controls ── */}
              <div className="w-full lg:w-[440px] shrink-0">
                <div className="glass-panel rounded-3xl p-5 sm:p-6">

                  {/* Mode selector (mobile only) */}
                  <div className="flex gap-2 mb-4 lg:hidden">
                    <button onClick={() => setMode("over")}
                      className={`flex-1 py-2 rounded-xl text-xs font-bold tracking-wider transition-all font-[family-name:var(--font-display)] cursor-pointer ${
                        mode === "over" ? "bg-purple-500/15 text-yellow-400 border border-yellow-400/30"
                          : "text-zinc-500 border border-purple-500/15 hover:text-white"
                      }`}>ROLL OVER</button>
                    <button onClick={() => setMode("under")}
                      className={`flex-1 py-2 rounded-xl text-xs font-bold tracking-wider transition-all font-[family-name:var(--font-display)] cursor-pointer ${
                        mode === "under" ? "bg-purple-500/15 text-yellow-400 border border-yellow-400/30"
                          : "text-zinc-500 border border-purple-500/15 hover:text-white"
                      }`}>ROLL UNDER</button>
                  </div>

                  {/* Threshold + slider */}
                  <div className="text-center mb-3">
                    <div className="text-[10px] tracking-[0.3em] text-yellow-300 uppercase font-bold font-[family-name:var(--font-display)] mb-1">
                      {mode === "over" ? "ROLL OVER" : "ROLL UNDER"}
                    </div>
                    <div className="text-[2.5rem] sm:text-[3rem] font-black text-[#90f085] leading-none font-[family-name:var(--font-title)]" style={{ textShadow: '0 0 30px rgba(144,240,133,0.2)' }}>
                      {threshold}
                    </div>
                  </div>
                  <div className="mb-4">
                    <input type="range" min={minChancePct} max={maxChancePct} value={threshold}
                      onChange={(e) => setThreshold(Number(e.target.value))}
                      className="w-full h-2 rounded-lg appearance-none cursor-pointer bg-purple-900/40 accent-[#90f085]"
                      style={{ accentColor: '#90f085' }} />
                    <div className="flex justify-between text-[10px] text-zinc-500 mt-1 font-mono">
                      <span>{minChancePct}</span><span>{maxChancePct}</span>
                    </div>
                  </div>

                  {/* Stats — compact row on mobile, 2x2 on desktop */}
                  <div className="flex gap-2 mb-4 lg:grid lg:grid-cols-2 lg:gap-3 lg:mb-6">
                    <div className="flex-1 glass-panel rounded-2xl p-3 text-center">
                      <div className="text-[9px] lg:text-[10px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">CHANCE</div>
                      <div className="text-sm lg:text-lg font-black text-white font-[family-name:var(--font-title)]">
                        {preview ? preview.chancePct.toFixed(1) : winChance.toFixed(1)}%
                      </div>
                    </div>
                    <div className="flex-1 glass-panel rounded-2xl p-3 text-center">
                      <div className="text-[9px] lg:text-[10px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">PAYOUT</div>
                      <div className="text-sm lg:text-lg font-black text-yellow-400 font-[family-name:var(--font-title)]">
                        {preview ? `${preview.multiplier.toFixed(2)}x` : "—"}
                      </div>
                    </div>
                    <div className="flex-1 glass-panel rounded-2xl p-3 text-center">
                      <div className="text-[9px] lg:text-[10px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">BET</div>
                      <div className="text-sm lg:text-lg font-black text-white font-[family-name:var(--font-title)]">
                        ${parseFloat(stakeInput || "0").toFixed(2)}
                      </div>
                    </div>
                    <div className="flex-1 glass-panel rounded-2xl p-3 text-center">
                      <div className="text-[9px] lg:text-[10px] tracking-[0.2em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-0.5">WIN</div>
                      <div className="text-sm lg:text-lg font-black text-white font-[family-name:var(--font-title)]">
                        {preview ? formatUSDC(preview.payoutUusdc.toString()) : "—"}
                      </div>
                    </div>
                  </div>

                  {/* Result (mobile only — between stats and stake) */}
                  {hasPlayed && (
                    <div className={`lg:hidden text-center mb-4 py-3 rounded-2xl border flex items-center justify-center gap-4 ${
                      betting ? 'border-purple-500/30 bg-purple-900/20' : isWin ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-red-500/30 bg-red-500/5'
                    }`}>
                      {betting ? (
                        <div className="flex items-center gap-2">
                          <Loader2 className="w-4 h-4 animate-spin text-purple-400/40" />
                          <span className="text-sm text-zinc-400 font-[family-name:var(--font-display)]">Rolling...</span>
                        </div>
                      ) : result && result.result ? (
                        <>
                          <div className={`text-3xl font-black font-[family-name:var(--font-title)] ${isWin ? "text-emerald-400" : "text-red-400"}`}>
                            {result.result.roll != null ? (result.result.roll / 100).toFixed(2) : "—"}
                          </div>
                          <div className="flex flex-col">
                            <div className={`text-sm font-black tracking-[0.1em] font-[family-name:var(--font-title)] ${isWin ? "text-emerald-400" : "text-red-400"}`}>
                              {isWin ? "WIN" : "LOSS"}
                            </div>
                            <div className={`text-xs font-bold font-[family-name:var(--font-display)] ${isWin ? "text-emerald-400/70" : "text-red-400/70"}`}>
                              {isWin
                                ? `+$${((parseInt(String(result.result?.payout || "0").replace("uusdc", "")) - parseInt(result.stake?.amount || "0")) / 1e6).toFixed(2)}`
                                : `-$${(parseInt(result.stake?.amount || "0") / 1e6).toFixed(2)}`}
                            </div>
                          </div>
                        </>
                      ) : null}
                    </div>
                  )}

                  {/* Stake selector */}
                  <div className="mb-4">
                    <div className="text-[10px] tracking-[0.25em] text-zinc-400 uppercase font-bold font-[family-name:var(--font-display)] mb-2">
                      STAKE
                    </div>
                    <div className="grid grid-cols-5 gap-1.5">
                      {["0.50", "1.00", "2.00", "5.00", "10.00"].map(v => (
                        <button key={v} onClick={() => setStakeInput(v)} disabled={betting}
                          className={`py-2 lg:py-2.5 rounded-2xl text-xs font-bold transition-all cursor-pointer font-[family-name:var(--font-display)] ${
                            stakeInput === v
                              ? "stake-chip-active"
                              : "bg-zinc-800 border border-yellow-400/30 text-zinc-400 hover:text-white hover:border-yellow-400/50"
                          } disabled:opacity-50 disabled:cursor-not-allowed`}>
                          ${v.replace(/\.00$/, "")}
                        </button>
                      ))}
                    </div>
                  </div>

                  {/* Error */}
                  {error && (
                    <div className="bg-red-500/10 border border-red-500/20 rounded-2xl px-4 py-2.5 mb-3">
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
                      className="w-full py-3 border border-emerald-500/30 bg-emerald-500/10 text-emerald-400 rounded-2xl text-sm font-bold hover:bg-emerald-500/20 transition-colors disabled:opacity-50 cursor-pointer flex items-center justify-center gap-2 mb-3 font-[family-name:var(--font-display)]">
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
                      className="w-full mb-3 py-3 border border-yellow-400/30 bg-yellow-400/10 text-yellow-400 rounded-2xl text-sm font-bold hover:bg-yellow-400/20 transition-colors disabled:opacity-50 cursor-pointer flex items-center justify-center gap-2 font-[family-name:var(--font-display)]">
                      {granting ? "Authorizing..." : "Authorize Instant Betting"}
                    </button>
                  )}

                  {/* ROLL DICE */}
                  {betting ? (
                    <button disabled
                      className="w-full mt-5 btn-disabled rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] text-purple-300 font-bold flex items-center justify-center gap-2 tracking-widest">
                      <Loader2 className="w-5 h-5 animate-spin" /> ROLLING...
                    </button>
                  ) : walletStatus === "none" ? (
                    <button onClick={handleBet}
                      className="w-full mt-5 btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest cursor-pointer flex items-center justify-center gap-2">
                      CREATE A WALLET
                    </button>
                  ) : walletStatus === "locked" ? (
                    <button onClick={handleBet}
                      className="w-full mt-5 btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest cursor-pointer flex items-center justify-center gap-2">
                      UNLOCK YOUR WALLET
                    </button>
                  ) : (
                    <button onClick={handleBet}
                      className="w-full mt-5 btn-start rounded-2xl py-4 text-lg font-[family-name:var(--font-title)] font-bold tracking-widest cursor-pointer flex items-center justify-center gap-2">
                      {hasPlayed ? "ROLL AGAIN" : "ROLL DICE"}
                    </button>
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
                        <th className="text-left px-3 py-2 font-bold">Roll</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Profit</th>
                        <th className="text-right px-3 py-2 font-bold">Result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const myBets = recentBets.filter(b => b.bettor === address);
                        if (myBets.length === 0) return (
                          <tr><td colSpan={4} className="text-center py-6 text-zinc-500">No bets yet</td></tr>
                        );
                        return myBets.slice(0, 500).map((b, i) => {
                          const profit = b.win ? (parseInt(b.payout) - parseInt(b.stake)) / 1e6 : -(parseInt(b.stake) / 1e6);
                          return (
                          <tr key={b.betId} className={`border-b border-zinc-800 ${i === 0 ? "animate-in" : ""} hover:bg-purple-500/5 transition-colors`}>
                            <td className="px-3 py-2 font-[family-name:var(--font-display)] text-zinc-300">
                              {b.roll != null ? (b.roll / 100).toFixed(2) : "—"}
                            </td>
                            <td className="px-3 py-2 text-right text-white font-bold font-[family-name:var(--font-display)]">
                              ${(parseInt(b.stake) / 1e6).toFixed(2)}
                            </td>
                            <td className={`px-3 py-2 text-right font-bold font-[family-name:var(--font-display)] ${profit >= 0 ? "text-emerald-400" : "text-red-400"}`}>
                              {profit >= 0 ? `+$${profit.toFixed(2)}` : `-$${Math.abs(profit).toFixed(2)}`}
                            </td>
                            <td className="px-3 py-2 text-right">
                              <span className={`inline-block px-2.5 py-0.5 rounded-full text-[10px] font-bold tracking-wider ${
                                b.win
                                  ? "bg-emerald-500/20 text-emerald-400"
                                  : "bg-red-500/20 text-red-400"
                              }`}>
                                {b.win ? "WIN" : "LOSS"}
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
                        <th className="text-right px-3 py-2 font-bold">Roll</th>
                        <th className="text-right px-3 py-2 font-bold">Stake</th>
                        <th className="text-right px-3 py-2 font-bold">Profit</th>
                        <th className="text-right px-3 py-2 font-bold">Result</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(() => {
                        const otherBets = recentBets.filter(b => b.bettor !== address);
                        if (otherBets.length === 0) return (
                          <tr><td colSpan={5} className="text-center py-6 text-zinc-500">Waiting for bets...</td></tr>
                        );
                        return otherBets.slice(0, 500).map((b, i) => {
                          const profit = b.win ? (parseInt(b.payout) - parseInt(b.stake)) / 1e6 : -(parseInt(b.stake) / 1e6);
                          return (
                          <tr key={b.betId} className={`border-b border-zinc-800 ${i === 0 ? "animate-in" : ""} hover:bg-purple-500/5 transition-colors`}>
                            <td className="px-3 py-2.5 font-[family-name:var(--font-display)] text-purple-400 text-[10px]">
                              {b.bettor.slice(0, 6)}..{b.bettor.slice(-3)}
                            </td>
                            <td className="px-3 py-2 text-right font-[family-name:var(--font-display)] text-zinc-300">
                              {b.roll != null ? (b.roll / 100).toFixed(2) : "—"}
                            </td>
                            <td className="px-3 py-2 text-right text-white font-bold font-[family-name:var(--font-display)]">
                              ${(parseInt(b.stake) / 1e6).toFixed(2)}
                            </td>
                            <td className={`px-3 py-2 text-right font-bold font-[family-name:var(--font-display)] ${profit >= 0 ? "text-emerald-400" : "text-red-400"}`}>
                              {profit >= 0 ? `+$${profit.toFixed(2)}` : `-$${Math.abs(profit).toFixed(2)}`}
                            </td>
                            <td className="px-3 py-2 text-right">
                              <span className={`inline-block px-2.5 py-0.5 rounded-full text-[10px] font-bold tracking-wider ${
                                b.win
                                  ? "bg-emerald-500/20 text-emerald-400"
                                  : "bg-red-500/20 text-red-400"
                              }`}>
                                {b.win ? "WIN" : "LOSS"}
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

export default function DicePage() {
  return (
    <Suspense fallback={<PagePreloader />}>
      <DiceGame />
    </Suspense>
  );
}
