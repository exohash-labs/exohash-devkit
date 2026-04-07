"use client";

import { useState } from "react";
import { useDice, useExo } from "@exohash/uikit";

const usdc = (u: number) => (u / 1_000_000).toFixed(2);

export default function DicePage() {
  const { address } = useExo();
  const dice = useDice();
  const [chance, setChance] = useState(50);
  const [stake, setStake] = useState("1000000");

  const handleRoll = async () => {
    await dice.roll(stake, chance * 100);
  };

  const shortAddr = (a: string) =>
    a === address ? "YOU" : a.length > 15 ? a.slice(0, 8) + "..." + a.slice(-4) : a;

  const multiplier = (9800 / (chance * 100)).toFixed(2);
  const maxWin = usdc(Math.floor(parseFloat(multiplier) * parseInt(stake || "0")));

  return (
    <div>
      <h2>Dice</h2>

      <div style={{ background: "#111", border: "1px solid #222", padding: 20, marginBottom: 20 }}>
        <div style={{ marginBottom: 16 }}>
          <label style={{ color: "#666", fontSize: 13 }}>CHANCE: {chance}%</label>
          <input type="range" min={1} max={98} value={chance} onChange={e => setChance(parseInt(e.target.value))} style={{ width: "100%", marginTop: 8 }} />
          <div style={{ display: "flex", justifyContent: "space-between", fontSize: 12, color: "#666" }}>
            <span>1%</span>
            <span>Multiplier: {multiplier}x</span>
            <span>98%</span>
          </div>
        </div>

        <div style={{ display: "flex", gap: 10, alignItems: "center" }}>
          <input type="text" value={stake} onChange={e => setStake(e.target.value)}
            style={{ background: "#0a0a0a", border: "1px solid #333", color: "#fff", padding: "8px 12px", fontFamily: "monospace", width: 150 }} />
          <span style={{ color: "#666", fontSize: 12 }}>
            ({usdc(parseInt(stake || "0"))} USDC → max win {maxWin})
          </span>
          <button onClick={handleRoll} disabled={dice.loading} style={{ background: "#34d399", color: "#000", border: "none", padding: "8px 24px", cursor: "pointer", fontFamily: "monospace" }}>
            {dice.loading ? "..." : "ROLL"}
          </button>
        </div>
        {dice.error && <div style={{ color: "#ef4444", fontSize: 12, marginTop: 8 }}>{dice.error}</div>}

        {/* My last result */}
        {dice.myLastResult && (
          <div style={{ marginTop: 12, padding: "8px 12px", background: dice.myLastResult.won ? "#0a1a0a" : "#1a0a0a", border: `1px solid ${dice.myLastResult.won ? "#1a4a1a" : "#4a1a1a"}` }}>
            <span style={{ color: dice.myLastResult.won ? "#34d399" : "#ef4444", fontWeight: "bold" }}>
              {dice.myLastResult.won ? "WIN" : "LOSS"}
            </span>
            <span style={{ color: "#888", marginLeft: 12 }}>
              Stake: {usdc(dice.myLastResult.stake)} → Payout: {usdc(dice.myLastResult.payout)}
            </span>
          </div>
        )}
      </div>

      <h3 style={{ color: "#666", fontSize: 13 }}>LIVE BETS ({dice.connected ? "LIVE" : "DISCONNECTED"})</h3>
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
        <thead>
          <tr style={{ borderBottom: "1px solid #222" }}>
            <th style={{ textAlign: "left", padding: 6, color: "#666" }}>Player</th>
            <th style={{ textAlign: "right", padding: 6, color: "#666" }}>Stake</th>
            <th style={{ textAlign: "right", padding: 6, color: "#666" }}>Result</th>
            <th style={{ textAlign: "right", padding: 6, color: "#666" }}>Payout</th>
          </tr>
        </thead>
        <tbody>
          {dice.recentBets.map(b => (
            <tr key={b.betId} style={{ borderBottom: "1px solid #111", background: b.isMe ? "#1a1a2a" : undefined }}>
              <td style={{ padding: 6, color: b.isMe ? "#a78bfa" : "#ccc", fontWeight: b.isMe ? "bold" : "normal" }}>{shortAddr(b.player)}</td>
              <td style={{ textAlign: "right", padding: 6 }}>{usdc(b.stake)}</td>
              <td style={{ textAlign: "right", padding: 6, color: b.won ? "#34d399" : "#ef4444" }}>{b.won ? "WIN" : "LOSS"}</td>
              <td style={{ textAlign: "right", padding: 6, color: b.payout > 0 ? "#34d399" : "#ef4444" }}>{usdc(b.payout)}</td>
            </tr>
          ))}
          {dice.recentBets.length === 0 && (
            <tr><td colSpan={4} style={{ padding: 20, textAlign: "center", color: "#666" }}>Waiting for bets...</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
