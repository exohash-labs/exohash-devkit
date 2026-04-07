"use client";

import { useBalance, useExo } from "@exohash/uikit";
import Link from "next/link";
import { useState } from "react";

export function Header() {
  const { client, address } = useExo();
  const { balance, refresh } = useBalance();
  const [faucetLoading, setFaucetLoading] = useState(false);

  const handleFaucet = async () => {
    if (!address) return;
    setFaucetLoading(true);
    try {
      await client.faucet(address);
      await refresh();
    } catch {
    } finally {
      setFaucetLoading(false);
    }
  };

  return (
    <div style={{ borderBottom: "1px solid #222", padding: "12px 20px", display: "flex", justifyContent: "space-between", alignItems: "center" }}>
      <div style={{ display: "flex", gap: 20, alignItems: "center" }}>
        <Link href="/" style={{ color: "#34d399", textDecoration: "none", fontWeight: "bold", fontSize: 16 }}>EXOHASH</Link>
        <Link href="/crash" style={{ color: "#999", textDecoration: "none" }}>Crash</Link>
        <Link href="/dice" style={{ color: "#999", textDecoration: "none" }}>Dice</Link>
        <Link href="/mines" style={{ color: "#999", textDecoration: "none" }}>Mines</Link>
      </div>
      <div style={{ display: "flex", gap: 16, alignItems: "center", fontSize: 13 }}>
        <span style={{ color: "#666" }}>{address}</span>
        <span style={{ color: "#fff" }}>{(parseInt(balance || "0") / 1_000_000).toFixed(2)} USDC</span>
        <button onClick={handleFaucet} disabled={faucetLoading} style={{ background: "#34d399", color: "#000", border: "none", padding: "4px 12px", cursor: "pointer", fontFamily: "monospace", fontSize: 12 }}>
          {faucetLoading ? "..." : "+ FAUCET"}
        </button>
      </div>
    </div>
  );
}
