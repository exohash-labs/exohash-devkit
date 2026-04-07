import Link from "next/link";

export default function Home() {
  return (
    <div style={{ textAlign: "center", paddingTop: 80 }}>
      <h1 style={{ color: "#34d399", fontSize: 28 }}>ExoHash Demo</h1>
      <p style={{ color: "#666", marginBottom: 16 }}>Raw UI — proves the hooks work. Designer takes it from here.</p>
      <p style={{ color: "#f59e0b", marginBottom: 40, fontSize: 14 }}>Tap + FAUCET to fund your account before playing.</p>
      <div style={{ display: "flex", gap: 20, justifyContent: "center" }}>
        <Link href="/crash" style={{ background: "#111", border: "1px solid #333", padding: "30px 40px", textDecoration: "none", color: "#fff", fontSize: 18 }}>
          Crash
        </Link>
        <Link href="/dice" style={{ background: "#111", border: "1px solid #333", padding: "30px 40px", textDecoration: "none", color: "#fff", fontSize: 18 }}>
          Dice
        </Link>
        <Link href="/mines" style={{ background: "#111", border: "1px solid #333", padding: "30px 40px", textDecoration: "none", color: "#fff", fontSize: 18 }}>
          Mines
        </Link>
      </div>
    </div>
  );
}
