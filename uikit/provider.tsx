import { createContext, useContext, useMemo, type ReactNode } from "react";
import { ExoClient } from "./client";

// ---------------------------------------------------------------------------
// ExoProvider — provides BFF client to all hooks via React context
// ---------------------------------------------------------------------------

interface ExoContextValue {
  client: ExoClient;
  bffUrl: string;
  address: string | null;
  bankrollId: number;
}

const ExoContext = createContext<ExoContextValue | null>(null);

export interface ExoProviderProps {
  /** BFF URL (e.g. "http://localhost:4000") */
  bffUrl: string;
  /** Player wallet address (null = not connected) */
  address?: string | null;
  /** Default bankroll ID (default 1) */
  bankrollId?: number;
  children: ReactNode;
}

export function ExoProvider({
  bffUrl,
  address = null,
  bankrollId = 1,
  children,
}: ExoProviderProps) {
  const value = useMemo<ExoContextValue>(
    () => ({
      client: new ExoClient(bffUrl),
      bffUrl,
      address,
      bankrollId,
    }),
    [bffUrl, address, bankrollId]
  );

  return <ExoContext.Provider value={value}>{children}</ExoContext.Provider>;
}

export function useExo(): ExoContextValue {
  const ctx = useContext(ExoContext);
  if (!ctx) {
    throw new Error("useExo must be used within <ExoProvider>");
  }
  return ctx;
}
