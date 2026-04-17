"use client";

import { useState, useEffect } from "react";
import { useWallet } from "@/contexts/WalletContext";
/** Call after a bet is placed to update the ExoScan bar. */
export function markBetPlaced(address: string) {
  localStorage.setItem(`exo_played_${address}`, "1");
  window.dispatchEvent(new Event("exohash:bet_placed"));
}

const SCAN_URL = process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io";

export function ExoScanBar() {
  const { address } = useWallet();
  const [hasBets, setHasBets] = useState(false);

  useEffect(() => {
    if (!address) { setHasBets(false); return; }
    setHasBets(!!localStorage.getItem(`exo_played_${address}`));
    const handler = () => setHasBets(true);
    window.addEventListener("exohash:bet_placed", handler);
    return () => window.removeEventListener("exohash:bet_placed", handler);
  }, [address]);

  return (
    <div className="w-full text-center py-1.5 text-[11px] bg-black/40 backdrop-blur-sm border-b border-white/5 relative z-10">
      {hasBets ? (
        <a
          href={`${SCAN_URL}/bets?bettor=${address}`}
          target="_blank"
          rel="noopener noreferrer"
          className="text-emerald-400/80 hover:text-emerald-300 transition-colors"
        >
          View your bets on ExoScan &rarr;
        </a>
      ) : (
        <a
          href={SCAN_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="text-white hover:text-emerald-400 transition-colors"
        >
          ExoScan — explore blocks, bets, beacon seeds &rarr;
        </a>
      )}
    </div>
  );
}
