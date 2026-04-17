"use client";

import { useWallet } from "@/contexts/WalletContext";
import { Wallet } from "lucide-react";

export function WalletButton() {
  const { status, address, openModal } = useWallet();

  if (status === "unlocked" && address) {
    return (
      <button
        onClick={() => openModal()}
        className="flex items-center gap-2 px-3 py-2 rounded-lg border border-white/10 bg-white/5 hover:border-yellow-500/30 transition-colors cursor-pointer"
      >
        <div className="w-2 h-2 rounded-full bg-emerald-400" />
        <span className="text-xs font-mono text-gray-300">
          {address.slice(0, 8)}...{address.slice(-4)}
        </span>
      </button>
    );
  }

  return (
    <button
      onClick={() => openModal()}
      className="flex items-center gap-2 px-4 py-2 rounded-lg bg-yellow-500 text-black text-sm font-bold hover:bg-yellow-400 transition-colors cursor-pointer"
    >
      <Wallet className="w-4 h-4" />
      {status === "locked" ? "Unlock" : "Connect"}
    </button>
  );
}
