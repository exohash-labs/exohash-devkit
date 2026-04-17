"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { flushSync } from "react-dom";
import type { PariRoundLive } from "./bff";

// SSE needs direct connection — Next.js proxy buffers SSE chunks.
const BFF_URL = typeof window !== "undefined"
  ? (process.env.NEXT_PUBLIC_BFF_DIRECT_URL || `${window.location.protocol}//${window.location.hostname}:3100`)
  : (process.env.BFF_URL || "http://localhost:3100");

/**
 * StreamEvent matches the BFF's SSE StreamEvent type.
 */
export interface StreamSettlement {
  betId: number;
  bettor: string;
  gameId: number;
  bankrollId: number;
  stake: string;        // gross stake uusdc
  netStake: string;     // after house edge
  payout: string;
  payoutAmount: string;
  payoutKind: number;   // 1=win, 2=loss, 3=refund
  result: string;
  reason: string;
  settlement?: Record<string, any>; // engine-specific result (dice: roll_bp, mult_bp, etc.)
}

export interface StreamCrashCashout {
  betId: number;
  multBP: number;       // 0 = busted (tick crashed)
  payoutAmount: string; // "0" if busted
}

export interface StreamCrashFeedEntry {
  kind: "join" | "cashout";
  bettor: string;
  betId: number;
  stake?: string;
  multBP?: number;
  payout?: string;
}

export interface StreamCrashRound {
  roundId: number;
  phase: string;
  currentMultBP: number;
  crashMultBP?: number;
  players: number;
  bets: number;
  totalStakedUusdc: string;
  survivors: number;
  closesInBlocks: number;
  cashouts?: StreamCrashCashout[];
  feed?: StreamCrashFeedEntry[];
}

export interface StreamGameDelta {
  gameId: number;
  engine: string;
  crashRound?: StreamCrashRound;
  pariRound?: PariRoundLive;
  bustHistory?: number[];
  totalBets: number;
  winRatePct: number;
}

export interface StreamPnL {
  block: number;
  bankrollPnl: string;
  valFees: string;
  protoFees: string;
  playerPnl: string;
  zeroSum: string;
  denom: string;
}

export interface StreamStepUpdate {
  betId: number;
  bettor: string;
  gameId: number;
  phase: string;
  gameState: Record<string, any>;
}

export interface StreamCalcEvent {
  calculatorId: number;
  topic: string;
  data: string; // raw JSON from WASM
}

export interface StreamEvent {
  height: number;
  time: string;
  txCount: number;
  games: StreamGameDelta[];
  settlements?: StreamSettlement[];
  calcEvents?: StreamCalcEvent[];
  stepUpdates?: StreamStepUpdate[];
  randomness?: { height: number; epoch: number; randomness: string };
  pnl?: StreamPnL;
}

type StreamStatus = "connecting" | "connected" | "reconnecting" | "error";

interface UseStreamReturn {
  status: StreamStatus;
  lastEvent: StreamEvent | null;
  lastHeight: number;
}

/**
 * useStream — connects to BFF SSE and returns the latest block event.
 * Auto-reconnects natively via EventSource spec.
 */
export function useStream(gameId?: number): UseStreamReturn {
  const [status, setStatus] = useState<StreamStatus>("connecting");
  const [lastEvent, setLastEvent] = useState<StreamEvent | null>(null);
  const [lastHeight, setLastHeight] = useState(0);
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    const url = gameId ? `${BFF_URL}/stream?games=${gameId}` : `${BFF_URL}/stream`;
    setStatus("connecting");

    const es = new EventSource(url);
    esRef.current = es;

    es.onopen = () => {
      setStatus("connected");
    };

    let isReplaying = false;
    let replayCalcEvents: StreamCalcEvent[] = [];

    es.onmessage = (ev) => {
      try {
        const raw = JSON.parse(ev.data);

        // Replay phase: buffer calc events, flush synchronously on replay end
        if (raw.connected && raw.replay === true) { isReplaying = true; return; }
        if (raw.replay === false) {
          isReplaying = false;
          if (replayCalcEvents.length > 0) {
            const events = replayCalcEvents.splice(0);
            flushSync(() => {
              setLastEvent({ height: -1, time: "", txCount: 0, games: [], calcEvents: events });
              setLastHeight(-1);
            });
          }
          return;
        }
        if (isReplaying) {
          if (raw.calcEvents) {
            for (const ce of raw.calcEvents) replayCalcEvents.push(ce);
          }
          return;
        }
        if (raw.heartbeat) return;

        // BFF sends betsSettled/betsCreated — normalize to settlements
        const data: StreamEvent = {
          ...raw,
          settlements: raw.settlements || raw.betsSettled,
          calcEvents: raw.calcEvents,
          stepUpdates: raw.stepUpdates || raw.stepResolved,
        };
        setLastEvent((prev) => {
          if (prev && prev.height === data.height) return prev;
          return data;
        });
        setLastHeight(data.height);
      } catch {
        // ignore parse errors
      }
    };

    es.onerror = (e) => {
      // EventSource auto-reconnects
      setStatus("reconnecting");
    };

    return () => {
      es.close();
      esRef.current = null;
    };
  }, [gameId]);

  return { status, lastEvent, lastHeight };
}

/**
 * useSettlementWatch — watches SSE stream for a specific bet settlement.
 * Returns the settlement when it arrives, or null.
 */
export function useSettlementWatch(
  stream: UseStreamReturn,
  betId: number | null,
  address: string | null
): StreamSettlement | null {
  const [settlement, setSettlement] = useState<StreamSettlement | null>(null);

  useEffect(() => {
    if (!betId || !address || !stream.lastEvent) return;
    const settlements = stream.lastEvent.settlements;
    if (!settlements) return;

    const match = settlements.find(
      (s) => s.betId === betId && s.bettor === address
    );
    if (match) {
      setSettlement(match);
    }
  }, [stream.lastEvent, betId, address]);

  return settlement;
}

/**
 * useCrashRound — extracts crash round state from stream for a given game ID.
 */
export function useCrashRound(
  stream: UseStreamReturn,
  gameId: number
): { round: StreamCrashRound | null; bustHistory: number[] } {
  const [round, setRound] = useState<StreamCrashRound | null>(null);
  const [bustHistory, setBustHistory] = useState<number[]>([]);

  useEffect(() => {
    if (!stream.lastEvent) return;
    const gameDelta = stream.lastEvent.games?.find((g) => g.gameId === gameId);
    if (!gameDelta) return;

    if (gameDelta.crashRound) {
      setRound((prev) => {
        const next = gameDelta.crashRound!;
        if (prev && prev.roundId === next.roundId &&
            prev.phase === next.phase &&
            prev.currentMultBP === next.currentMultBP &&
            prev.players === next.players &&
            prev.bets === next.bets &&
            prev.closesInBlocks === next.closesInBlocks) {
          return prev;
        }
        return next;
      });
    }
    if (gameDelta.bustHistory && gameDelta.bustHistory.length > 0) {
      setBustHistory((prev) => {
        if (prev.length === gameDelta.bustHistory!.length && prev[0] === gameDelta.bustHistory![0]) {
          return prev;
        }
        return gameDelta.bustHistory!;
      });
    }
  }, [stream.lastEvent?.height, gameId]);

  return { round, bustHistory };
}
