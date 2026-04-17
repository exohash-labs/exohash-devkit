"use client";

import { useEffect, useRef, useState } from "react";

const BFF_URL = typeof window !== "undefined"
  ? (process.env.NEXT_PUBLIC_BFF_DIRECT_URL || `${window.location.protocol}//${window.location.hostname}:3100`)
  : "http://localhost:3100";

export interface CalcEvent {
  calculatorId: number;
  topic: string;
  data: string;
}

/**
 * useBetFeed — connects to BFF SSE, buffers replay, streams live calcEvents.
 *
 * Uses useRef for data (immune to React batching) + counter for re-renders.
 * Each game provides its own parser and topic filter.
 *
 * @param gameId - calculator ID to filter events
 * @param topics - calc event topics to include (e.g. ["settle"], ["joined","cashout","settled"])
 * @param parse  - converts a raw calcEvent into a bet entry (return null to skip)
 * @param maxEntries - max entries to keep (default 500)
 */
export function useBetFeed<T>(
  gameId: number,
  topics: string[],
  parse: (ce: CalcEvent, data: any) => T | null,
  maxEntries = 500,
): T[] {
  const betsRef = useRef<T[]>([]);
  const [, setTick] = useState(0);

  useEffect(() => {
    const url = `${BFF_URL}/stream?games=${gameId}`;
    const es = new EventSource(url);
    let replaying = false;
    const buf: T[] = [];

    es.onmessage = (ev) => {
      let raw: any;
      try { raw = JSON.parse(ev.data); } catch { return; }

      // Replay start
      if (raw.connected && raw.replay === true) { replaying = true; return; }

      // Replay end — flush buffer, single render
      if (raw.replay === false) {
        replaying = false;
        betsRef.current = buf.slice(0, maxEntries);
        setTick(t => t + 1);
        return;
      }

      if (raw.heartbeat) return;

      // Parse matching calcEvents
      let added = false;
      for (const ce of raw.calcEvents || []) {
        if (ce.calculatorId !== gameId || !topics.includes(ce.topic)) continue;
        let d: any;
        try { d = JSON.parse(ce.data); } catch { continue; }
        const entry = parse(ce, d);
        if (entry) {
          buf.unshift(entry);
          added = true;
        }
      }

      if (replaying) return; // don't render during replay

      if (added) {
        if (buf.length > maxEntries) buf.length = maxEntries;
        betsRef.current = buf.slice(0, maxEntries);
        setTick(t => t + 1);
      }
    };

    return () => es.close();
  }, [gameId]); // eslint-disable-line react-hooks/exhaustive-deps

  return betsRef.current;
}
