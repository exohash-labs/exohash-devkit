import { useState, useCallback, useRef } from "react";
import { usePlaceBet } from "./usePlaceBet";
import { useBalance } from "./useBalance";
import { useStream } from "./useStream";
import type { StreamEvent } from "../types";

const DICE_CALC_ID = 1;

export interface DiceResult {
  betId: number;
  player: string;
  stake: number;
  payout: number;
  won: boolean;
  isMe: boolean;
}

export function useDice() {
  const [recentBets, setRecentBets] = useState<DiceResult[]>([]);
  const [myLastResult, setMyLastResult] = useState<DiceResult | null>(null);
  const { send: placeBet, loading, error } = usePlaceBet();
  const { balance, refresh: refreshBalance } = useBalance();

  // Track betsCreated across SSE messages (bet created in block N, settled in N+1).
  const betInfo = useRef<Record<number, { player: string; stake: number }>>({});
  const myBetIds = useRef<Set<number>>(new Set());

  const handleEvent = useCallback((ev: StreamEvent, isReplay: boolean) => {
    // Accumulate bet info from betsCreated.
    for (const bc of ev.betsCreated ?? []) {
      betInfo.current[bc.betId] = { player: bc.bettor, stake: Number(bc.stake) };
    }

    // Process settlements.
    for (const bs of ev.betsSettled ?? []) {
      if (bs.gameId !== DICE_CALC_ID) continue;
      const info = betInfo.current[bs.betId];
      const isMe = !isReplay && myBetIds.current.has(bs.betId);
      const result: DiceResult = {
        betId: bs.betId,
        player: info?.player ?? `#${bs.betId}`,
        stake: info?.stake ?? 0,
        payout: Number(bs.payout),
        won: bs.payoutKind === 1,
        isMe,
      };
      setRecentBets(prev => [result, ...prev].slice(0, 30));
      if (isMe) {
        setMyLastResult(result);
        myBetIds.current.delete(bs.betId);
        refreshBalance();
      }
      // Cleanup.
      delete betInfo.current[bs.betId];
    }

    // Prune stale betInfo entries to prevent unbounded growth.
    const infoKeys = Object.keys(betInfo.current);
    if (infoKeys.length > 200) {
      for (const key of infoKeys.slice(0, infoKeys.length - 100)) {
        delete betInfo.current[Number(key)];
      }
    }
  }, [refreshBalance]);

  const { connected, height } = useStream([DICE_CALC_ID], handleEvent);

  const roll = useCallback(
    async (stake: string, chanceBp: number) => {
      const params = new Array(9).fill(0);
      params[0] = 2; // mode = over
      let v = chanceBp;
      for (let i = 1; i < 9; i++) {
        params[i] = v & 0xff;
        v = Math.floor(v / 256);
      }
      const res = await placeBet({ calculatorId: DICE_CALC_ID, stake, params });
      if (res) {
        myBetIds.current.add(res.betId);
      }
      return res;
    },
    [placeBet]
  );

  return {
    roll,
    recentBets,
    myLastResult,
    connected,
    height,
    balance,
    loading,
    error,
  };
}
