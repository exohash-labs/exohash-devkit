"use client";

import { useState, useEffect, useCallback } from "react";
import { Wallet, Droplets, Dice5, Zap, ShieldCheck } from "lucide-react";
import Link from "next/link";
import { Header } from "@/components/Header";
import { Footer } from "@/components/Footer";
import { PagePreloader } from "@/components/PagePreloader";
import { useWallet } from "@/contexts/WalletContext";
import {
  bff,
  formatUSDC,
  getUSDCBalance,
  type StatusResponse,
  type BalanceResponse,
} from "@/lib/bff";
import { ensureRelayGrant } from "@/lib/signer";

export default function HomePage() {
  const { ready: walletReady, status: walletStatus, address, wallet, openModal } = useWallet();

  const [chainStatus, setChainStatus] = useState<StatusResponse | null>(null);
  const [balance, setBalance] = useState<BalanceResponse | null>(null);
  const [faucetLoading, setFaucetLoading] = useState(false);
  const [faucetMsg, setFaucetMsg] = useState("");

  useEffect(() => {
    bff.status().then(setChainStatus).catch(() => {});
    const id = setInterval(() => {
      bff.status().then(setChainStatus).catch(() => {});
    }, 5000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    if (walletStatus !== "unlocked" || !address) return;
    bff.balance(address).then(setBalance).catch(() => {});
    const id = setInterval(() => {
      if (address) bff.balance(address).then(setBalance).catch(() => {});
    }, 5000);
    return () => clearInterval(id);
  }, [walletStatus, address]);

  const usdcBalance = balance ? getUSDCBalance(balance.balances) : "0";

  const handleFaucet = useCallback(async () => {
    if (!address) return;
    setFaucetLoading(true);
    setFaucetMsg("");
    try {
      await bff.faucet(address);
      setFaucetMsg("Tokens sent!");
      setTimeout(() => {
        if (address) bff.balance(address).then(setBalance).catch(() => {});
      }, 2000);
      if (wallet) {
        await ensureRelayGrant(wallet, address);
      }
    } catch {
      setFaucetMsg("Faucet error — try again");
    }
    setFaucetLoading(false);
  }, [address, wallet]);

  const gameCards = [
    {
      id: "mines",
      thumb: "/mines-thumb.webp",
      title: "Mines",
      type: "Step Game",
      desc: "Open tiles, avoid mines, cash out anytime. Each step multiplies your payout. Every tile resolved by on-chai...",
      href: "/mines",
    },
    {
      id: "dice",
      thumb: "/dice-thumb-new.webp",
      title: "Dice",
      type: "Instant Bet",
      desc: "Pick your chance, roll the dice. Higher risk, higher reward. Every roll resolved by on-chain BLS randomness.",
      href: "/dice",
    },
    {
      id: "crash",
      thumb: "/crash-thumb-new.webp",
      title: "Crash",
      type: "Multiplayer Game",
      desc: "Watch the multiplier climb. Cash out before it crashes. The longer you wait, the bigger the win — or you lose it all.",
      href: "/crash",
    },
  ];

  if (!walletReady) return <PagePreloader />;

  return (
    <div className="min-h-screen flex flex-col relative page-bg">
      <Header balance={usdcBalance} />

      {/* ── MAIN ── */}
      <main className="flex-1 relative z-10 pt-[56px]">
        <div className="max-w-[960px] mx-auto px-6">

          {/* ── HERO ── */}
          <section className="text-center pt-4 sm:pt-6 pb-0">
            {/* DevNet badge */}
            <div className="animate-in delay-1 mb-1 inline-flex items-center gap-2.5 px-5 py-2 rounded-full border border-purple-400/25 bg-purple-400/[0.06]">
              <div className="w-2 h-2 rounded-full bg-purple-400 live-dot" />
              <span className="text-[11px] tracking-[0.25em] text-purple-400 uppercase font-bold font-[family-name:var(--font-title)]">
                ExoHash — DevNet
              </span>
            </div>
            {chainStatus && (
              <div className="animate-in delay-1 mb-1 flex items-center justify-center gap-2 text-[12px] text-zinc-500 font-mono">
                Block {chainStatus.chain.blockHeight.toLocaleString()} · 500ms finality ·
                <span className="inline-flex items-center gap-1 text-emerald-400 text-[10px] font-bold tracking-wider uppercase"><span className="w-1.5 h-1.5 rounded-full bg-emerald-400 live-dot" />live</span>
              </div>
            )}
            <div className="animate-in delay-1 mb-3 text-[11px] text-zinc-500 font-mono">
              Games backed by{" "}
              <a
                href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bankrolls/1`}
                target="_blank"
                rel="noreferrer"
                className="text-yellow-400/80 hover:text-yellow-300 transition-colors"
              >
                OpenExo bankroll
              </a>
              {" "}— verify on ExoScan
            </div>

            <h1 className="animate-in delay-2 text-[2.4rem] sm:text-[3.2rem] font-bold text-white leading-[1.05] mb-2 font-[family-name:var(--font-title)]" style={{ letterSpacing: '-0.02em', textShadow: '0 2px 30px rgba(0,0,0,0.7), 0 4px 60px rgba(0,0,0,0.3)' }}>
              Play real games.
              <br />
              <span className="neon-gold">
                On a real blockchain.
              </span>
            </h1>

            {walletStatus === "none" && (
              <div className="animate-in delay-3 mt-4 mb-2 max-w-md mx-auto glass-panel rounded-2xl p-4 border-yellow-400/20 bg-yellow-400/[0.04]">
                <p className="text-[14px] text-zinc-300 leading-relaxed font-[family-name:var(--font-display)] mb-3">
                  Your wallet lives in your browser — no extensions, no custody risk. Get free test USDC and play instantly.
                </p>
                <button
                  onClick={openModal}
                  className="w-full btn-start inline-flex items-center justify-center gap-3 px-10 py-3 rounded-2xl text-white font-bold text-[15px] cursor-pointer uppercase tracking-wider font-[family-name:var(--font-title)]"
                >
                  <Wallet className="w-5 h-5" />
                  Create Wallet
                </button>
              </div>
            )}

            <div className="animate-in delay-5 mt-4 mb-2">
              {walletStatus === "none" ? null : walletStatus === "locked" ? (
                <button
                  onClick={openModal}
                  className="btn-start inline-flex items-center gap-3 px-10 py-3 rounded-2xl text-white font-bold text-[15px] cursor-pointer uppercase tracking-wider font-[family-name:var(--font-title)]"
                >
                  <Wallet className="w-5 h-5" />
                  Unlock Wallet
                </button>
              ) : Number(usdcBalance) / 1e6 < 5 ? (
                <div>
                  <button
                    onClick={handleFaucet}
                    disabled={faucetLoading}
                    className="btn-start inline-flex items-center gap-3 px-10 py-3 rounded-2xl text-white font-bold text-[15px] disabled:opacity-50 cursor-pointer uppercase tracking-wider font-[family-name:var(--font-title)]"
                  >
                    <Droplets className="w-5 h-5" />
                    {faucetLoading ? "Sending..." : "Get Test USDC"}
                  </button>
                  {faucetMsg && (
                    <p className={`mt-3 text-xs ${faucetMsg.includes("error") ? "text-red-400" : "text-emerald-400"}`}>
                      {faucetMsg}
                    </p>
                  )}
                </div>
              ) : null}
            </div>
          </section>

          {/* ── GAMES ── */}
          <section className="animate-in delay-4 pt-4 pb-8">
            <div className="flex items-center gap-4 justify-center mb-4">
              <div className="h-px flex-1 max-w-[80px]" style={{ background: 'rgba(107, 33, 168, 0.3)' }} />
              <h2 className="text-center text-[1.6rem] sm:text-[1.85rem] font-extrabold text-yellow-400 tracking-[0.2em] font-[family-name:var(--font-title)] uppercase neon-gold">
                Games
              </h2>
              <div className="h-px flex-1 max-w-[80px]" style={{ background: 'rgba(107, 33, 168, 0.3)' }} />
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-3 gap-5">
              {gameCards.map((game) => (
                <Link
                  key={game.id}
                  href={game.href}
                  className="glass-panel rounded-3xl group block overflow-hidden hover:border-purple-400/30 hover:shadow-[0_0_30px_rgba(107,33,168,0.2)] transition-all"
                >
                  <div className="flex items-start justify-between p-5 pb-2">
                    <div>
                      <h3 className="text-2xl font-black text-white font-[family-name:var(--font-title)] tracking-wide">
                        {game.title}
                      </h3>
                      <span className="inline-block mt-1 px-2 py-0.5 rounded text-[9px] tracking-wider uppercase font-bold text-yellow-400 border border-yellow-400/30 bg-yellow-400/10">
                        {game.type}
                      </span>
                    </div>
                    <img
                      src={game.thumb}
                      alt={game.title}
                      className="w-[90px] h-[90px] object-cover rounded-2xl shrink-0 group-hover:scale-105 transition-transform drop-shadow-[0_4px_20px_rgba(107,33,168,0.4)]"
                    />
                  </div>
                  <div className="px-5 pb-5">
                    <p className="text-[13px] text-zinc-300 leading-[1.6]">
                      {game.desc}
                    </p>
                  </div>
                </Link>
              ))}
            </div>
          </section>

          {/* ── FEATURES ── */}
          <section className="pb-10">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
              {[
                {
                  icon: <Dice5 className="w-7 h-7 text-yellow-400 drop-shadow-[0_0_8px_rgba(234,179,8,0.5)]" />,
                  title: "Provably Fair",
                  desc: "BLS threshold randomness — every outcome verifiable on-chain",
                },
                {
                  icon: <Zap className="w-7 h-7 text-yellow-400 drop-shadow-[0_0_8px_rgba(234,179,8,0.5)]" />,
                  title: "Instant Settlement",
                  desc: "Sub-second finality. No waiting, no confirmations",
                },
                {
                  icon: <ShieldCheck className="w-7 h-7 text-yellow-400 drop-shadow-[0_0_8px_rgba(234,179,8,0.5)]" />,
                  title: "Fully Collateralized",
                  desc: "Protocol-enforced solvency — every payout guaranteed",
                },
              ].map((f) => (
                <div
                  key={f.title}
                  className="glass-panel rounded-2xl p-5 hover:border-purple-400/25 transition-colors"
                >
                  <div className="mb-3">{f.icon}</div>
                  <h3 className="text-sm font-bold text-white mb-1 font-[family-name:var(--font-title)]">
                    {f.title}
                  </h3>
                  <p className="text-xs text-zinc-400 leading-relaxed">{f.desc}</p>
                </div>
              ))}
            </div>
          </section>

          {/* ── FOR OPERATORS ── */}
          <section className="pb-10">
            <div className="glass-panel rounded-3xl p-6 sm:p-8">
              <span className="text-[10px] tracking-[0.3em] text-purple-400 uppercase font-bold font-[family-name:var(--font-title)]">
                For Operators
              </span>
              <h3 className="text-base font-bold text-white mt-1.5 mb-2 font-[family-name:var(--font-title)]">
                Run Your Own Gaming Platform
              </h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-xl mb-4">
                Create a bankroll, attach games, set your house edge. Your players bet — the protocol handles
                randomness, settlement, and solvency. Every bet is provably fair. Every payout is fully
                collateralized. You keep the edge.
              </p>
              <a
                href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bankrolls/1`}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 text-sm text-yellow-400 hover:text-yellow-300 transition-colors font-bold cursor-pointer"
              >
                View on ExoScan →
              </a>
            </div>
          </section>

          {/* ── TECH BADGES ── */}
          <div className="flex flex-wrap items-center justify-center gap-2 pb-10">
            {[
              "Cosmos SDK",
              "BLS Threshold Randomness",
              "Protocol-Enforced Solvency",
              "500ms Finality",
              "USDC Denominated",
            ].map((tag) => (
              <span
                key={tag}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full border border-purple-400/30 text-[10px] text-zinc-300 tracking-[0.15em] uppercase bg-purple-900/[0.95]"
              >
                <span className="w-1 h-1 rounded-full bg-purple-400/40" />
                {tag}
              </span>
            ))}
          </div>
        </div>

        {/* Bug report bar */}
      </main>

      <Footer />
    </div>
  );
}
