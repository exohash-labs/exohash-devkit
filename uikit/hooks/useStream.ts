import { useEffect, useRef, useState, useCallback } from "react";
import { useExo } from "../provider";
import { ExoStream } from "../stream";
import type { StreamEvent } from "../types";

export function useStream(
  games?: number[],
  onEvent?: (ev: StreamEvent, isReplay: boolean) => void,
) {
  const { bffUrl, address } = useExo();
  const [connected, setConnected] = useState(false);
  const [height, setHeight] = useState(0);
  const streamRef = useRef<ExoStream | null>(null);
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  const handleEvent = useCallback((ev: StreamEvent, isReplay: boolean) => {
    if (ev.height) setHeight(ev.height);
    onEventRef.current?.(ev, isReplay);
  }, []);

  useEffect(() => {
    const stream = new ExoStream({
      baseUrl: bffUrl,
      games,
      onEvent: handleEvent,
      onStatus: setConnected,
    });

    stream.connect();
    streamRef.current = stream;

    return () => stream.disconnect();
  }, [bffUrl, JSON.stringify(games), address, handleEvent]);

  return { connected, height };
}
