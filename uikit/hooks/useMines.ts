import { useState, useCallback, useEffect, useRef } from "react";
import { useExo } from "../provider";
import { usePlaceBet } from "./usePlaceBet";
import { useBetAction } from "./useBetAction";
import { useBalance } from "./useBalance";
import { useStream } from "./useStream";
import type { StreamEvent, CalcEvent, BetState } from "../types";

const MINES_CALC_ID = 3;
const BOARD_SIZE = 25;

export type TileState = "hidden" | "safe" | "mine" | "pending";

export interface MinesState {
  betId: number | null;
  active: boolean;
  mines: number;
  revealed: number;
  multiplier: number;   // basis points
  payout: number;       // current cashout value in uusdc
  board: TileState[];
  result: "playing" | "won" | "lost" | "refunded" | null;
}

function emptyBoard(): TileState[] {
  return new Array(BOARD_SIZE).fill("hidden");
}

const INITIAL_STATE: MinesState = {
  betId: null,
  active: false,
  mines: 3,
  revealed: 0,
  multiplier: 10000,
  payout: 0,
  board: emptyBoard(),
  result: null,
};

export function useMines() {
  const { client, address } = useExo();
  const [state, setState] = useState<MinesState>(INITIAL_STATE);
  const [log, setLog] = useState<string[]>([]);

  const { send: placeBet, loading: betLoading, error: betError } = usePlaceBet();
  const { send: betAction, loading: actionLoading, error: actionError } = useBetAction();
  const { balance, refresh: refreshBalance } = useBalance();

  // Ownership tracking. Set from HTTP response, cleared on settlement.
  const myBetRef = useRef<number | null>(null);
  const addrRef = useRef(address);
  addrRef.current = address;
  const coldStartDone = useRef(false);

  // -------------------------------------------------------------------------
  // SSE event processing
  // -------------------------------------------------------------------------

  const processCalcEvent = useCallback((ce: CalcEvent, isReplay: boolean) => {
    if (ce.calculatorId !== MINES_CALC_ID) return;

    let data: any;
    try { data = JSON.parse(ce.data); } catch { return; }

    // Update MY game state — only for live events matching my bet.
    if (!isReplay && data.bet_id === myBetRef.current) {
      switch (ce.topic) {
        case "joined":
          // Already handled optimistically by start(). SSE confirms.
          break;

        case "reveal": {
          setState(prev => {
            const board = [...prev.board];
            if (data.safe === 1) {
              board[data.tile] = "safe";
              return {
                ...prev,
                board,
                revealed: data.revealed,
                multiplier: data.mult_bp,
                payout: data.payout,
              };
            } else {
              board[data.tile] = "mine";
              return {
                ...prev,
                board,
                active: false,
                revealed: data.revealed,
                result: "lost",
              };
            }
          });
          break;
        }

        case "settled": {
          let result: "won" | "lost" | "refunded" = "won";
          if (data.kind === 2) result = "lost";
          if (data.kind === 3) result = "refunded";
          myBetRef.current = null;
          setState(prev => ({
            ...prev,
            active: false,
            payout: data.payout,
            result,
          }));
          refreshBalance();
          break;
        }
      }
    }

    // Activity log — always (live + replay).
    const who = (a: string) => a === addrRef.current ? "YOU" : a && a.length > 15 ? a.slice(0, 8) + "..." : a;
    switch (ce.topic) {
      case "joined":
        setLog(p => [`+ ${who(data.addr)} mines=${data.mines}`, ...p].slice(0, 50));
        break;
      case "reveal":
        setLog(p => [`${data.safe ? "SAFE" : "MINE"} tile=${data.tile} ${who(data.addr)}`, ...p].slice(0, 50));
        break;
      case "settled":
        setLog(p => [`${data.reason}: ${(data.payout / 1e6).toFixed(2)} ${who(data.addr)}`, ...p].slice(0, 50));
        break;
      case "state":
        // Skip noisy per-block ticks — only log meaningful transitions
        break;
    }
  }, [refreshBalance]);

  const handleStreamEvent = useCallback(
    (ev: StreamEvent, isReplay: boolean) => {
      for (const ce of ev.calcEvents ?? []) {
        processCalcEvent(ce, isReplay);
      }
      if (!isReplay && ev.betsSettled?.length) {
        refreshBalance();
      }
    },
    [processCalcEvent, refreshBalance]
  );

  const { connected, height } = useStream([MINES_CALC_ID], handleStreamEvent);

  // -------------------------------------------------------------------------
  // Cold start: restore open bet on mount
  // -------------------------------------------------------------------------

  useEffect(() => {
    if (!address || coldStartDone.current) return;
    coldStartDone.current = true;

    (async () => {
      try {
        const bets = await client.bets(address);
        const openMines = bets.find(
          (b) => b.gameId === MINES_CALC_ID && b.status === "open"
        );
        if (!openMines) return;
        const betState: BetState = await client.betState(openMines.betId);
        if (betState.status !== "open") return;

        // Set ref BEFORE processing events so isMine matches.
        myBetRef.current = openMines.betId;
        for (const ev of betState.events) {
          // Process as live (not replay) since these are for our active bet.
          processCalcEvent(ev, false);
        }
      } catch {
        // no open bet or API unavailable
      }
    })();
  }, [address, client, processCalcEvent]);

  // -------------------------------------------------------------------------
  // Actions
  // -------------------------------------------------------------------------

  const start = useCallback(
    async (stake: string, minesCount: number) => {
      myBetRef.current = null;
      const res = await placeBet({
        calculatorId: MINES_CALC_ID,
        stake,
        params: [minesCount],
      });
      if (res) {
        myBetRef.current = res.betId;
        setState({
          betId: res.betId,
          active: true,
          mines: minesCount,
          revealed: 0,
          multiplier: 10000,
          payout: 0,
          board: emptyBoard(),
          result: "playing",
        });
      }
      return res;
    },
    [placeBet]
  );

  const reveal = useCallback(
    async (tile: number) => {
      const betId = myBetRef.current;
      if (!betId) return false;
      setState(prev => {
        const board = [...prev.board];
        board[tile] = "pending";
        return { ...prev, board };
      });
      return betAction(betId, [1, tile]);
    },
    [betAction]
  );

  const cashout = useCallback(async () => {
    const betId = myBetRef.current;
    if (!betId) return false;
    const ok = await betAction(betId, [2]);
    if (ok) refreshBalance();
    return ok;
  }, [betAction, refreshBalance]);

  return {
    ...state,
    connected,
    height,
    balance,
    log,
    start,
    reveal,
    cashout,
    loading: betLoading || actionLoading,
    error: betError || actionError,
  };
}
