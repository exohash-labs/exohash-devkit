import { useState, useEffect, useCallback, useRef } from "react";
import { useExo } from "../provider";

const POLL_INTERVAL = 15000;

export function useBalance() {
  const { client, address } = useExo();
  const [balance, setBalance] = useState<string>("0");
  const [loading, setLoading] = useState(false);
  const mountedRef = useRef(true);

  const refresh = useCallback(async () => {
    if (!address) return;
    setLoading(true);
    try {
      const res = await client.balance(address);
      if (mountedRef.current) setBalance(res.usdc);
    } catch {
      // network error — keep stale balance, poll will retry
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, [client, address]);

  useEffect(() => {
    mountedRef.current = true;
    refresh();
    const id = setInterval(refresh, POLL_INTERVAL);
    return () => {
      mountedRef.current = false;
      clearInterval(id);
    };
  }, [refresh]);

  return { balance, loading, refresh };
}
