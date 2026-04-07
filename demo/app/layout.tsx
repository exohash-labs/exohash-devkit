"use client";

import { useState, useEffect } from "react";
import { ExoProvider } from "@exohash/uikit";
import { Header } from "./header";

const BFF_URL = process.env.NEXT_PUBLIC_BFF_URL || "http://localhost:4000";

function useAddress() {
  const [address, setAddress] = useState<string | null>(null);

  useEffect(() => {
    let addr = localStorage.getItem("exo_address");
    if (!addr) {
      const bytes = crypto.getRandomValues(new Uint8Array(20));
      addr = "exo1" + Array.from(bytes).map(b => b.toString(16).padStart(2, "0")).join("");
      localStorage.setItem("exo_address", addr);
    }
    setAddress(addr);
  }, []);

  return address;
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  const address = useAddress();

  return (
    <html lang="en">
      <body style={{ margin: 0, background: "#0a0a0a", color: "#e5e5e5", fontFamily: "monospace" }}>
        {address ? (
          <ExoProvider bffUrl={BFF_URL} address={address} bankrollId={1}>
            <Header />
            <main style={{ padding: 20, maxWidth: 900, margin: "0 auto" }}>
              {children}
            </main>
          </ExoProvider>
        ) : (
          <div style={{ padding: 40, textAlign: "center", color: "#666" }}>Loading...</div>
        )}
      </body>
    </html>
  );
}
