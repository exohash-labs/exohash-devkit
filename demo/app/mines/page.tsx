"use client";

import { useState } from "react";
import { useMines, useExo } from "@exohash/uikit";

const usdc = (u: number) => (u / 1_000_000).toFixed(2);
const fmt = (bp: number) => (bp / 10000).toFixed(2) + "x";

export default function MinesPage() {
  const { address } = useExo();
  const mines = useMines();
  const [stakeInput, setStakeInput] = useState("1000000");
  const [minesInput, setMinesInput] = useState(3);

  const handleStart = async () => {
    if (mines.loading || mines.active) return;
    const res = await mines.start(stakeInput, minesInput);
    if (!res) {
      console.log("mines start failed:", mines.error);
    }
  };

  return (
    <div style={{ display: "flex", gap: 16, fontSize: 12 }}>
      <div style={{ flex: 1 }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
          <h2 style={{ fontSize: 16, margin: 0 }}>Mines</h2>
          <span style={{ color: mines.active ? "#34d399" : "#666" }}>
            {mines.result === "playing" ? "PLAYING" : mines.result === "won" ? "WON" : mines.result === "lost" ? "LOST" : mines.result === "refunded" ? "REFUNDED" : "IDLE"}
          </span>
          <span style={{ color: "#666" }}>h={mines.height} {mines.connected ? "LIVE" : "DISCONNECTED"}</span>
        </div>

        {/* Board */}
        <div style={{ display: "grid", gridTemplateColumns: "repeat(5, 52px)", gap: 3, marginBottom: 12 }}>
          {mines.board.map((tile, i) => (
            <button
              key={i}
              onClick={() => mines.reveal(i)}
              disabled={tile !== "hidden" || !mines.active || mines.loading}
              style={{
                width: 52, height: 52,
                border: "1px solid #333",
                fontFamily: "monospace",
                fontSize: 16,
                cursor: tile === "hidden" && mines.active ? "pointer" : "default",
                background:
                  tile === "safe" ? "#065f46" :
                  tile === "mine" ? "#7f1d1d" :
                  tile === "pending" ? "#44403c" :
                  "#111",
                color:
                  tile === "safe" ? "#34d399" :
                  tile === "mine" ? "#ef4444" :
                  tile === "pending" ? "#f59e0b" :
                  "#444",
              }}
            >
              {tile === "safe" ? "+" : tile === "mine" ? "X" : tile === "pending" ? "?" : i}
            </button>
          ))}
        </div>

        {/* Info bar */}
        {mines.active && (
          <div style={{ display: "flex", gap: 12, alignItems: "center", padding: "8px 0", borderTop: "1px solid #222", borderBottom: "1px solid #222", marginBottom: 12 }}>
            <span>Revealed: <b style={{ color: "#34d399" }}>{mines.revealed}</b></span>
            <span>Mult: <b style={{ color: "#f59e0b" }}>{fmt(mines.multiplier)}</b></span>
            <span>Payout: <b style={{ color: "#34d399" }}>{usdc(mines.payout)} USDC</b></span>
            <span style={{ flex: 1 }} />
            <button
              onClick={mines.cashout}
              disabled={mines.loading || mines.revealed === 0}
              style={{
                background: mines.revealed > 0 ? "#f59e0b" : "#333",
                color: "#000", border: "none", padding: "6px 16px",
                fontWeight: "bold", cursor: mines.revealed > 0 ? "pointer" : "default",
                fontFamily: "monospace",
              }}
            >
              CASHOUT {usdc(mines.payout)}
            </button>
          </div>
        )}

        {/* Start controls */}
        {!mines.active && (
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginBottom: 12 }}>
            <input
              type="text" value={stakeInput} onChange={e => setStakeInput(e.target.value)}
              style={{ width: 80, background: "#1a1a1a", border: "1px solid #333", color: "#fff", padding: "4px 6px", fontSize: 12, fontFamily: "monospace" }}
            />
            <select
              value={minesInput} onChange={e => setMinesInput(parseInt(e.target.value))}
              style={{ background: "#1a1a1a", border: "1px solid #333", color: "#fff", padding: "4px 6px", fontFamily: "monospace" }}
            >
              {[1,2,3,4,5,6,7,8,9,10,11,12,13].map(n => (
                <option key={n} value={n}>{n} mines</option>
              ))}
            </select>
            <button
              onClick={handleStart} disabled={mines.loading}
              style={{ background: "#34d399", color: "#000", border: "none", padding: "6px 16px", fontWeight: "bold", cursor: "pointer", fontFamily: "monospace" }}
            >
              START
            </button>

            {/* Last result */}
            {mines.result && mines.result !== "playing" && (
              <span style={{
                color: mines.result === "won" ? "#34d399" : mines.result === "lost" ? "#ef4444" : "#f59e0b",
                fontWeight: "bold", marginLeft: 8,
              }}>
                {mines.result.toUpperCase()} — {usdc(mines.payout)} USDC
              </span>
            )}
          </div>
        )}

        {mines.error && <div style={{ color: "#ef4444", fontSize: 11, marginBottom: 8 }}>{mines.error}</div>}

        {/* State dump */}
        <pre style={{
          background: "#0d0d0d", border: "1px solid #222", padding: 10,
          fontSize: 10, color: "#8b8", overflow: "auto", maxHeight: 200,
        }}>
{JSON.stringify({
  betId: mines.betId,
  active: mines.active,
  mines: mines.mines,
  revealed: mines.revealed,
  multiplier: fmt(mines.multiplier),
  payout: usdc(mines.payout),
  result: mines.result,
  board: mines.board.map((t, i) => t !== "hidden" ? `${i}:${t}` : null).filter(Boolean),
}, null, 2)}
        </pre>
      </div>

      {/* Event log */}
      <div style={{ width: 280, borderLeft: "1px solid #222", paddingLeft: 12 }}>
        <h3 style={{ fontSize: 13, color: "#888" }}>EVENT LOG ({mines.connected ? "LIVE" : "DISCONNECTED"})</h3>
        <div style={{ fontSize: 11, lineHeight: 1.6 }}>
          {mines.log.map((l, i) => (
            <div key={i} style={{
              color: l.startsWith("SAFE") ? "#34d399" :
                l.startsWith("MINE") ? "#ef4444" :
                l.startsWith("+") ? "#60a5fa" :
                l.includes("cashout") ? "#f59e0b" :
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
