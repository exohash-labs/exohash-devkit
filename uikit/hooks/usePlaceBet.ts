import { useState, useCallback } from "react";
import { useExo } from "../provider";
import type { PlaceBetResponse } from "../types";
import { ExoApiError } from "../client";

export function usePlaceBet() {
  const { client, address, bankrollId } = useExo();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const send = useCallback(
    async (params: {
      calculatorId: number;
      stake: string;
      params: number[];
    }): Promise<PlaceBetResponse | null> => {
      if (!address) {
        setError("Wallet not connected");
        return null;
      }
      setLoading(true);
      setError(null);
      try {
        const res = await client.placeBet({
          address,
          bankrollId,
          ...params,
        });
        return res;
      } catch (e) {
        const msg = e instanceof ExoApiError ? e.message : "Failed to place bet";
        setError(msg);
        return null;
      } finally {
        setLoading(false);
      }
    },
    [client, address, bankrollId]
  );

  const clearError = useCallback(() => setError(null), []);

  return { send, loading, error, clearError };
}
