import { useState, useCallback } from "react";
import { useExo } from "../provider";
import { ExoApiError } from "../client";

export function useBetAction() {
  const { client, address } = useExo();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const send = useCallback(
    async (betId: number, action: number[]): Promise<boolean> => {
      if (!address) {
        setError("Wallet not connected");
        return false;
      }
      setLoading(true);
      setError(null);
      try {
        await client.betAction({ address, betId, action });
        return true;
      } catch (e) {
        const msg = e instanceof ExoApiError ? e.message : "Action failed";
        setError(msg);
        return false;
      } finally {
        setLoading(false);
      }
    },
    [client, address]
  );

  const clearError = useCallback(() => setError(null), []);

  return { send, loading, error, clearError };
}
