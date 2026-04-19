"use client";

import { useState, useEffect, useRef } from "react";
import { bff, type BetResponse } from "./bff";
import type { StreamEvent } from "./useStream";

/**
 * useWaitForBet — watches for bet updates via SSE.
 *
 * Supports two matching modes:
 * - By address+gameId: starts matching immediately (before relay returns betId)
 * - By betId: strict match for step updates (mines tiles)
 *
 * For instant games (dice): address match resolves the result as soon as SSE
 * delivers the settlement — no need to wait for the relay response.
 */
export function useWaitForBet(
  betId: number | null,
  targetPhases: string[],
  lastEvent: StreamEvent | null,
  address?: string | null,
  gameId?: number | null,
): { bet: BetResponse | null; waiting: boolean } {
  const [bet, setBet] = useState<BetResponse | null>(null);
  const [waiting, setWaiting] = useState(false);
  const doneRef = useRef(false);
  const betIdRef = useRef(betId);
  const addressRef = useRef(address);

  // Reset when betId or address changes (new bet in flight)
  useEffect(() => {
    const newBet = betId !== betIdRef.current;
    const newAddr = address !== addressRef.current;
    betIdRef.current = betId;
    addressRef.current = address;
    if (newBet || newAddr) {
      doneRef.current = false;
      setBet(null);
      setWaiting(!!(betId || address));
    }
  }, [betId, address, targetPhases]);

  // SSE-driven: watch settlements and step updates
  useEffect(() => {
    if (doneRef.current) return;
    if (!lastEvent) return;
    const watchAddr = addressRef.current;
    const watchBetId = betIdRef.current;
    if (!watchBetId && !watchAddr) return;

    // Match settlement by betId (exact) or by address+gameId (early match)
    const settlement = lastEvent.settlements?.find((s: any) => {
      if (watchBetId && s.betId === watchBetId) return true;
      if (watchAddr && gameId && s.bettor === watchAddr && s.gameId === gameId) return true;
      return false;
    });

    if (settlement) {
      const matchedBy = watchBetId && settlement.betId === watchBetId ? "betId" : "address";
      console.log(`[useWaitForBet] matched by ${matchedBy} (betId=${settlement.betId}, addr=${watchAddr?.slice(0,10)})`);
      const matchedBetId = settlement.betId;
      const calcEvent = lastEvent.games?.[0]?.crashRound ? null :
        (lastEvent as any).calcEvents?.find((e: any) => {
          try { return JSON.parse(e.data).bet_id === matchedBetId; } catch { return false; }
        });
      let engineResult: any = null;
      if (calcEvent) {
        try { engineResult = JSON.parse(calcEvent.data); } catch {}
      }

      const payout = Number(settlement.payout || settlement.payoutAmount || 0);
      const netStake = Number((settlement as any).netStake || 0);
      const isWin = payout > 0;

      doneRef.current = true;
      setBet({
        id: matchedBetId,
        bankrollId: settlement.bankrollId,
        gameId: settlement.gameId,
        engine: "",
        bettor: settlement.bettor,
        stake: { denom: "uusdc", amount: String(netStake || 0) },
        phase: "GAME_PHASE_DONE",
        result: {
          win: isWin,
          payout: String(payout) + "uusdc",
          ...(engineResult || {}),
        },
      });
      setWaiting(false);
      return;
    }

    // Check intermediate step updates (mines tile resolve, etc.) — betId only
    if (watchBetId) {
      const step = lastEvent.stepUpdates?.find((s) => s.betId === watchBetId);
      if (step && targetPhases.includes(step.phase)) {
        setBet((prev) => ({
          id: step.betId,
          bankrollId: prev?.bankrollId ?? 0,
          gameId: step.gameId,
          engine: prev?.engine ?? "",
          bettor: step.bettor,
          stake: prev?.stake ?? { denom: "", amount: "" },
          phase: step.phase,
          gameState: step.gameState,
        }));
        setWaiting(false);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastEvent?.height, betId, address, gameId]);

  return { bet, waiting };
}
