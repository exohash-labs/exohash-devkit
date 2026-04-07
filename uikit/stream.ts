import type { StreamEvent } from "./types";

// ---------------------------------------------------------------------------
// ExoStream — SSE client with auto-reconnect and replay handling
// ---------------------------------------------------------------------------

export type StreamCallback = (event: StreamEvent, isReplay: boolean) => void;

export interface ExoStreamOptions {
  /** BFF base URL */
  baseUrl: string;
  /** Game IDs to subscribe to (undefined = all) */
  games?: number[];
  /** Player address to filter bets (undefined = all) */
  address?: string;
  /** Called for each event (live or replay). isReplay=true during replay phase. */
  onEvent: StreamCallback;
  /** Called when connection state changes */
  onStatus?: (connected: boolean) => void;
  /** Reconnect delay in ms (default 2000) */
  reconnectMs?: number;
}

export class ExoStream {
  private es: EventSource | null = null;
  private opts: ExoStreamOptions;
  private replayBuf: StreamEvent[] = [];
  private replaying = false;

  constructor(opts: ExoStreamOptions) {
    this.opts = opts;
  }

  connect(): void {
    if (this.es) this.disconnect();

    const params = new URLSearchParams();
    if (this.opts.games?.length) {
      params.set("games", this.opts.games.join(","));
    }
    if (this.opts.address) {
      params.set("address", this.opts.address);
    }

    const qs = params.toString();
    const url = `${this.opts.baseUrl}/stream${qs ? `?${qs}` : ""}`;

    this.es = new EventSource(url);
    this.replayBuf = [];
    this.replaying = false;

    this.es.onmessage = (msg) => {
      try {
        const ev: StreamEvent = JSON.parse(msg.data);
        this.handleEvent(ev);
      } catch {
        // ignore unparseable messages
      }
    };

    this.es.onerror = () => {
      this.opts.onStatus?.(false);
      this.disconnect();
      // Auto-reconnect.
      setTimeout(() => this.connect(), this.opts.reconnectMs ?? 2000);
    };
  }

  disconnect(): void {
    if (this.es) {
      this.es.close();
      this.es = null;
    }
  }

  private handleEvent(ev: StreamEvent): void {
    // Connection established — start replay phase.
    if (ev.connected && ev.replay) {
      this.replaying = true;
      this.replayBuf = [];
      this.opts.onStatus?.(true);
      return;
    }

    // End of replay — flush buffer as replay events, then switch to live.
    if (ev.replay === false) {
      this.replaying = false;
      for (const re of this.replayBuf) {
        this.opts.onEvent(re, true);
      }
      this.replayBuf = [];
      return;
    }

    // Heartbeat — ignore (connection is alive).
    if (ev.heartbeat) {
      return;
    }

    // During replay — buffer.
    if (this.replaying) {
      this.replayBuf.push(ev);
      return;
    }

    // Live event.
    this.opts.onEvent(ev, false);
  }
}
