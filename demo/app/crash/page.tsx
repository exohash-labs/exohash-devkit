"use client";

import { useState } from "react";
import { useCrash, useExo, type CrashPlayer } from "@exohash/uikit";

const fmt = (bp: number) => (bp / 10000).toFixed(2) + "x";
const usdc = (u: number) => (u / 1e6).toFixed(2);

export default function CrashPage() {
  const { address } = useExo();
  const crash = useCrash();
  const [stakeInput, setStakeInput] = useState("1000000");

  const canJoin = crash.phase === "open" && !crash.myResult;
  const canCashout = crash.phase === "tick" && crash.myResult === "playing";

  const handleJoin = async () => {
    await crash.join(stakeInput);
  };

  const phaseColor =
    crash.phase === "crashed" ? "#ef4444" :
    crash.phase === "open" ? "#f59e0b" :
    crash.phase === "tick" ? "#34d399" : "#666";

  const shortAddr = (a: string) =>
    a === address ? "YOU" : a.length > 15 ? a.slice(0, 8) + "..." + a.slice(-4) : a;

  return (
    <div style={{ display: "flex", gap: 16, fontSize: 12 }}>
      <div style={{ flex: 1 }}>
        {/* Header */}
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
          <h2 style={{ fontSize: 16, margin: 0 }}>Crash -- Round #{crash.round}</h2>
          <span style={{ color: phaseColor, fontWeight: "bold", textTransform: "uppercase" }}>{crash.phase}</span>
          <span style={{ color: "#666" }}>h={crash.height} {crash.connected ? "LIVE" : "DISCONNECTED"}</span>
        </div>

        {/* Big multiplier */}
        <div style={{
          textAlign: "center", padding: 20, marginBottom: 12,
          background: "#111", border: "1px solid #333",
          fontSize: 48, fontWeight: "bold", color: phaseColor,
        }}>
          {crash.phase === "open" ? `${crash.blocksLeft} blocks` : fmt(crash.multiplier)}
        </div>

        {/* Controls */}
        <div style={{ display: "flex", gap: 8, marginBottom: 12, alignItems: "center", padding: "8px 0", borderTop: "1px solid #222", borderBottom: "1px solid #222" }}>
          {canJoin && <>
            <input value={stakeInput} onChange={e => setStakeInput(e.target.value)} type="number" step="100000" min="100000"
              style={{ width: 80, background: "#1a1a1a", border: "1px solid #333", color: "#fff", padding: "4px 6px", fontSize: 12 }} />
            <button onClick={handleJoin} disabled={crash.loading}
              style={{ background: "#34d399", color: "#000", border: "none", padding: "6px 16px", fontWeight: "bold", fontSize: 13, cursor: "pointer" }}>
              JOIN
            </button>
          </>}

          {canCashout &&
            <button onClick={crash.cashout} disabled={crash.loading}
              style={{ background: "#f59e0b", color: "#000", border: "none", padding: "8px 24px", fontWeight: "bold", fontSize: 16, cursor: "pointer" }}>
              CASHOUT {fmt(crash.multiplier)}
            </button>
          }

          {crash.myResult === "playing" && crash.phase === "open" &&
            <span style={{ color: "#34d399", fontSize: 11 }}>Joined -- waiting for start...</span>}
          {crash.myResult === "cashed" &&
            <span style={{ color: "#f59e0b", fontWeight: "bold" }}>CASHED OUT</span>}
          {crash.myResult === "bust" &&
            <span style={{ color: "#ef4444", fontWeight: "bold" }}>BUST</span>}

          <span style={{ flex: 1 }} />

          <span style={{ color: "#aaa" }}>
            Active: <b style={{ color: "#34d399" }}>{crash.active}</b>
            {" "} Cashed: <b style={{ color: "#f59e0b" }}>{crash.cashed}</b>
          </span>
        </div>

        {crash.error && <div style={{ color: "#ef4444", fontSize: 11, marginBottom: 8 }}>{crash.error}</div>}

        {/* Players table */}
        {crash.players.length > 0 && (
          <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 11, marginBottom: 16 }}>
            <thead>
              <tr style={{ borderBottom: "1px solid #333", color: "#666", textAlign: "left" }}>
                <th style={{ padding: "6px 8px" }}>Player</th>
                <th style={{ padding: "6px 8px" }}>Stake</th>
                <th style={{ padding: "6px 8px" }}>Status</th>
                <th style={{ padding: "6px 8px" }}>Cashout</th>
                <th style={{ padding: "6px 8px" }}>Payout</th>
              </tr>
            </thead>
            <tbody>
              {crash.players.map(p => {
                const isMe = p.addr === address;
                const status = p.cashoutMult ? "cashed" : (crash.phase === "crashed" && p.payout !== null) ? "bust" : "playing";
                const statusColor = status === "cashed" ? "#f59e0b" : status === "bust" ? "#ef4444" : "#34d399";
                return (
                  <tr key={p.id} style={{ borderBottom: "1px solid #1a1a1a", background: isMe ? "#1a1a2a" : undefined }}>
                    <td style={{ padding: "4px 8px", fontWeight: isMe ? "bold" : "normal", color: isMe ? "#a78bfa" : undefined }}>
                      {shortAddr(p.addr)}
                    </td>
                    <td style={{ padding: "4px 8px" }}>{usdc(p.stake)}</td>
                    <td style={{ padding: "4px 8px", color: statusColor }}>{status}</td>
                    <td style={{ padding: "4px 8px" }}>{p.cashoutMult ? fmt(p.cashoutMult) : "-"}</td>
                    <td style={{ padding: "4px 8px", color: p.payout && p.payout > 0 ? "#34d399" : p.payout === 0 ? "#ef4444" : "#888" }}>
                      {p.payout !== null ? p.payout > 0 ? usdc(p.payout) : "0" : "-"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}

        {/* Crash history */}
        {crash.history.length > 0 && (
          <div>
            <h3 style={{ fontSize: 13, color: "#888", marginBottom: 8 }}>Crash History</h3>
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
              {crash.history.map((h, i) => (
                <span key={i} style={{
                  padding: "3px 8px", fontSize: 11,
                  background: h < 15000 ? "#1a0a0a" : h < 20000 ? "#1a1a0a" : "#0a1a0a",
                  border: `1px solid ${h < 15000 ? "#4a1a1a" : h < 20000 ? "#4a4a1a" : "#1a4a1a"}`,
                  color: h < 15000 ? "#ef4444" : h < 20000 ? "#eab308" : "#34d399",
                }}>
                  {fmt(h)}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Event log — raw SSE feed */}
      <div style={{ width: 350, borderLeft: "1px solid #222", paddingLeft: 12 }}>
        <h3 style={{ fontSize: 13, color: "#888" }}>EVENT LOG ({crash.connected ? "LIVE" : "DISCONNECTED"})</h3>
        <div style={{ fontSize: 11, lineHeight: 1.6 }}>
          {crash.log.map((l, i) => (
            <div key={i} style={{
              color: l.startsWith("CRASH") ? "#ef4444" :
                l.startsWith("ROUND") ? "#34d399" :
                l.startsWith("$") ? "#f59e0b" :
                l.startsWith("+") ? "#60a5fa" :
                l.includes("x t=") ? "#444" :
                "#888"
            }}>
              {l}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
