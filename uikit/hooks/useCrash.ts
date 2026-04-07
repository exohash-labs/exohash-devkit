import { useState, useCallback, useRef, useEffect } from "react";
import { useStream } from "./useStream";
import { usePlaceBet } from "./usePlaceBet";
import { useBetAction } from "./useBetAction";
import { useExo } from "../provider";
import type { StreamEvent } from "../types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export type CrashPhase = "waiting" | "open" | "tick" | "crashed";

export interface CrashPlayer {
  id: number;
  addr: string;
  stake: number;
  cashoutMult: number | null;
  payout: number | null;
}

export interface CrashState {
  round: number;
  phase: CrashPhase;
  multiplier: number;
  tick: number;
  blocksLeft: number;
  active: number;
  cashed: number;
  crashPoint: number;
  history: number[];
  players: CrashPlayer[];
  myBetId: number | null;
  myResult: "playing" | "cashed" | "bust" | null;
}

const CALC_ID = 2;

const INITIAL: CrashState = {
  round: 0,
  phase: "waiting",
  multiplier: 10000,
  tick: 0,
  blocksLeft: 0,
  active: 0,
  cashed: 0,
  crashPoint: 0,
  history: [],
  players: [],
  myBetId: null,
  myResult: null,
};

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export function useCrash() {
  const { client, address } = useExo();
  const [state, setState] = useState<CrashState>(INITIAL);
  const [log, setLog] = useState<string[]>([]);
  const { send: placeBet, loading: betLoading, error: betError } = usePlaceBet();
  const { send: betAction, loading: actionLoading, error: actionError } = useBetAction();

  const myBetRef = useRef<number | null>(null);
  const addrRef = useRef(address);
  addrRef.current = address;

  const fmt = (bp: number) => (bp / 10000).toFixed(2) + "x";
  const short = (a: string) => a && a.length > 15 ? a.slice(0, 8) + "..." + a.slice(-4) : a;

  const handleEvent = useCallback((ev: StreamEvent, isReplay: boolean) => {
    for (const ce of ev.calcEvents ?? []) {
      if (ce.calculatorId !== CALC_ID) continue;

      let d: any;
      try { d = JSON.parse(ce.data); } catch { continue; }

      switch (ce.topic) {
        case "state":
          setState(prev => {
            const phase = d.phase as CrashPhase;
            const next: CrashState = {
              ...prev,
              round: d.round,
              phase,
              multiplier: d.mult_bp,
              tick: d.tick,
              blocksLeft: d.blocks_left,
              active: d.active,
              cashed: d.cashed,
            };
            if (phase === "crashed" && prev.phase !== "crashed") {
              return {
                ...next,
                crashPoint: d.mult_bp,
                history: [d.mult_bp, ...prev.history].slice(0, 20),
                myResult: prev.myResult === "playing" ? "bust" : prev.myResult,
                players: prev.players.map(p =>
                  p.cashoutMult === null ? { ...p, payout: 0 } : p
                ),
              };
            }
            if (phase === "crashed" && prev.phase === "crashed") {
              return next;
            }
            if (phase === "open" && prev.round !== d.round) {
              // New round — reset players and own bet state.
              myBetRef.current = null;
              return { ...next, players: [], myBetId: null, myResult: null };
            }
            return next;
          });
          // Log — avoid duplicates for multi-block phases.
          if (!isReplay) {
            setLog(prev => {
              if (d.phase === "tick") {
                return [`${fmt(d.mult_bp)} t=${d.tick} [${d.active}/${d.active + d.cashed}]`, ...prev].slice(0, 100);
              }
              if (d.phase === "crashed" && !prev[0]?.startsWith("CRASH")) {
                return [`CRASH ${fmt(d.mult_bp)} tick=${d.tick}`, ...prev].slice(0, 100);
              }
              if (d.phase === "open" && !prev[0]?.startsWith("ROUND")) {
                return [`ROUND #${d.round}`, ...prev].slice(0, 100);
              }
              return prev;
            });
          }
          break;

        case "joined":
          setState(prev => {
            if (prev.players.some(p => p.id === d.bet_id)) return prev;
            return {
              ...prev,
              players: [...prev.players, {
                id: d.bet_id,
                addr: d.addr,
                stake: d.stake,
                cashoutMult: null,
                payout: null,
              }],
            };
          });
          setLog(p => [`+ ${short(d.addr)} ${(d.stake / 1e6).toFixed(2)}`, ...p].slice(0, 100));
          break;

        case "cashout":
          setState(prev => ({
            ...prev,
            myResult: d.bet_id === myBetRef.current ? "cashed" as const : prev.myResult,
            players: prev.players.map(p =>
              p.id === d.bet_id ? { ...p, cashoutMult: d.mult_bp, payout: d.payout } : p
            ),
          }));
          setLog(p => [`$ ${short(d.addr)} @${fmt(d.mult_bp)} = ${(d.payout / 1e6).toFixed(2)}`, ...p].slice(0, 100));
          break;

        case "settled":
          setState(prev => ({
            ...prev,
            players: prev.players.map(p =>
              p.id === d.bet_id ? { ...p, payout: d.payout } : p
            ),
          }));
          break;
      }
    }
  }, []);

  const { connected, height } = useStream([CALC_ID], handleEvent);

  // Cold-start: restore active bet on mount.
  useEffect(() => {
    if (!address) return;
    client.bets(address).then(bets => {
      const open = bets.find(b => b.gameId === CALC_ID && b.status === "open");
      if (open) {
        myBetRef.current = open.betId;
        setState(prev => ({ ...prev, myBetId: open.betId, myResult: "playing" }));
      }
    }).catch(() => {});
  }, [address, client]);

  // Actions.

  const join = useCallback(async (stake: string) => {
    const res = await placeBet({ calculatorId: CALC_ID, stake, params: [] });
    if (res) {
      myBetRef.current = res.betId;
      setState(prev => ({ ...prev, myBetId: res.betId, myResult: "playing" }));
    }
    return res;
  }, [placeBet]);

  const cashout = useCallback(async () => {
    const betId = myBetRef.current;
    if (!betId) return false;
    return betAction(betId, [1]);
  }, [betAction]);

  return {
    ...state,
    connected,
    height,
    log,
    join,
    cashout,
    loading: betLoading || actionLoading,
    error: betError || actionError,
  };
}
