"use client";

import { WalletProvider } from "@/contexts/WalletContext";
import { WalletModal } from "@/components/WalletModal";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <WalletProvider>
      {children}
      <WalletModal />
    </WalletProvider>
  );
}
