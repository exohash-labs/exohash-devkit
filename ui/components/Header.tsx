"use client";

import Link from "next/link";
import { Wallet } from "lucide-react";
import { useWallet } from "@/contexts/WalletContext";
import { formatUSDC } from "@/lib/bff";

export function Header({ balance }: { balance?: string }) {
  const { status: walletStatus, address, openModal } = useWallet();
  const displayBalance = balance ?? "0";

  return (
    <header className="fixed top-0 z-50 w-full bg-[#0a0515]/70 backdrop-blur-xl border-b border-purple-500/10">
      <div className="mx-auto flex h-[56px] max-w-[1340px] items-center justify-between px-6">
        <Link href="/" className="flex items-center">
          <img src="/exohash-logo.png" alt="EXOHASH" className="h-[28px]" />
        </Link>

        <div className="flex items-center gap-3">
          {walletStatus === "unlocked" && address ? (
            <button
              onClick={openModal}
              className="flex items-center gap-2.5 px-5 py-2.5 rounded-full border border-yellow-400/30 bg-yellow-400/5 hover:bg-yellow-400/15 transition-all cursor-pointer"
            >
              <div className="w-1.5 h-1.5 rounded-full bg-yellow-400 live-dot" />
              <span className="text-xs text-yellow-400 font-bold">{formatUSDC(displayBalance)} USDC</span>
              <span className="text-[10px] text-white/30 font-mono hidden sm:inline">
                {address.slice(0, 6)}···{address.slice(-4)}
              </span>
            </button>
          ) : (
            <button
              onClick={openModal}
              className="flex items-center gap-2.5 px-6 py-2.5 rounded-full bg-zinc-900/80 border border-yellow-400/30 text-yellow-400 text-[13px] font-semibold tracking-wide hover:bg-yellow-400/10 transition-all cursor-pointer font-[family-name:var(--font-display)]"
            >
              <Wallet className="w-4 h-4 opacity-80" />
              {walletStatus === "locked" ? "UNLOCK WALLET" : "CREATE WALLET"}
            </button>
          )}
        </div>
      </div>
    </header>
  );
}
