/**
 * BFF API client for play.exohash.io
 */

const BFF_URL = process.env.NEXT_PUBLIC_BFF_URL || "/api/bff";

// Direct BFF connection for latency-critical calls (skips Next.js proxy)
const BFF_DIRECT = typeof window !== "undefined"
  ? (process.env.NEXT_PUBLIC_BFF_DIRECT_URL || `${window.location.protocol}//${window.location.hostname}:3100`)
  : (process.env.BFF_URL || "http://localhost:3100");

export interface BankrollInfo {
  id: number;
  name: string;
  creator: string;
  balance: string; // uusdc
  available: string;
  denom: string;
  utilizationPct: number;
  isPrivate: boolean;
  gameIds: number[];
}

export interface GameInfo {
  id: number;
  name: string;
  bankrollId: number;
  engine: string;
  configJson: string;
}

export interface ChainInfo {
  chainId: string;
  blockHeight: number;
  blockTime: string;
  blockIntervalMs: number;
}

export interface StatusResponse {
  chain: ChainInfo;
  bankrolls: BankrollInfo[];
  games: GameInfo[];
}

export interface BalanceResponse {
  address: string;
  balances: { denom: string; amount: string }[];
}

export interface BetResponse {
  id: number;
  bankrollId: number;
  gameId: number;
  engine: string;
  bettor: string;
  stake: { denom: string; amount: string };
  phase: string;
  gameState?: any;
  result?: any;
}

// --- Parimutuel types ---

export interface PariOutcome {
  id: string;
  name?: string;
  pool: string; // uusdc
  payoutX: string; // e.g. "1.82"
  state?: { currentHp?: number; eliminated?: boolean; [key: string]: any };
}

export interface PariRoundLive {
  roundId: number;
  phase: string; // OPEN | LIVE | SETTLED
  outcomes: PariOutcome[];
  ticksElapsed: number;
  maxTicks: number;
  winnerIdx: number; // -1 if not settled
  winnerId?: string;
  totalPool: string; // uusdc
  players: number;
  bets: number;
  closesInBlocks: number;
  houseEdgeBP: number;
  bettingOpen: boolean;
  visualization?: string;
  recentEntries?: PariEntry[];
}

export interface PariEntry {
  betId: number;
  bettor: string;
  outcomeId: string;
  stake: string; // uusdc amount
  height: number;
}

export interface GameLiveResponse {
  gameId: number;
  engine: string;
  pariRound?: PariRoundLive;
  crashRound?: any;
  recentBets?: { id: number; bettor: string; stake: { denom: string; amount: string }; result?: { win: boolean } }[];
  totalBets: number;
  winRatePct: number;
}

export interface RelayResult {
  txHash: string;
  betId?: number;
  roundId?: number;
}

// --- Fetch helpers ---

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${BFF_URL}${path}`, { cache: "no-store" });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || res.statusText);
  }
  return res.json();
}

async function postDirect<T>(path: string, body: any): Promise<T> {
  const res = await fetch(`${BFF_DIRECT}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    let msg = text;
    try { msg = JSON.parse(text).error || text; } catch {}
    throw new Error(msg);
  }
  return res.json();
}

async function post<T>(path: string, body: any): Promise<T> {
  const res = await fetch(`${BFF_URL}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    let msg = text;
    try {
      msg = JSON.parse(text).error || text;
    } catch {}
    throw new Error(msg);
  }
  return res.json();
}

// --- API ---

export const bff = {
  status: async (): Promise<StatusResponse> => {
    // Real BFF has /health + /games, not /status. Build StatusResponse from both.
    const [health, games] = await Promise.all([
      get<{ games: number; height: number; status: string }>("/health"),
      get<{ calcId: number; name: string; engine: string; houseEdgeBp: number; status: number }[]>("/games"),
    ]);
    return {
      chain: { chainId: "exohash-solo-1", blockHeight: health.height, blockTime: "", blockIntervalMs: 1000 },
      bankrolls: [],
      games: games.map(g => ({ id: g.calcId, name: g.name, bankrollId: g.calcId, engine: g.engine, configJson: "" })),
    };
  },
  games: async () => {
    const raw = await get<{ calcId: number; name: string; engine: string; houseEdgeBp: number; status: number }[]>("/games");
    return raw.map(g => ({ id: g.calcId, name: g.name, bankrollId: g.calcId, engine: g.engine, configJson: "", houseEdgeBp: g.houseEdgeBp, status: g.status }));
  },
  balance: async (addr: string): Promise<BalanceResponse> => {
    const raw = await get<{ address: string; usdc: string }>(`/account/${addr}/balance`);
    return { address: raw.address, balances: [{ denom: "uusdc", amount: raw.usdc }] };
  },
  faucet: (addr: string) => post<{ status: string; tx: string }>("/faucet/request", { address: addr }),
  broadcast: (txBytes: string) => post<any>("/tx/broadcast", { tx_bytes: txBytes }),
  bet: (id: number) => get<BetResponse>(`/bet/${id}/state`),
  playerBets: (addr: string, limit = 20) => get<{ bets: BetResponse[] }>(`/account/${addr}/bets?limit=${limit}`),
  gamePreview: (_gameId: number, _params: string) => Promise.resolve(null as any),
  gameSchema: (gameId: number) => get<any>(`/games/${gameId}/schema`),
  gameLive: (gameId: number) => get<GameLiveResponse>(`/game/${gameId}/info`).catch(() => ({ recentBets: [] }) as any),
  recentBets: (gameId: number, limit = 20) => get<{ betId: number; gameId: number; bettor: string; stake: number; payout: number; status: string }[]>(`/bets/recent?game=${gameId}&limit=${limit}`),

  // Relay endpoints — bets via authz (no client-side signing per bet)
  relayInfo: () => get<{ enabled: boolean; relayAddress: string }>("/relay/info"),
  relayPlaceBet: (params: {
    address: string;
    bankrollId: number;
    gameId: number;
    stake: string;
    gameState: any;
  }) => postDirect<RelayResult>("/relay/place-bet", {
    address: params.address,
    bankrollId: params.bankrollId,
    calculatorId: params.gameId,
    stake: params.stake,
    params: params.gameState,
  }),
  relayGameAction: (params: {
    address: string;
    betId: number;
    action: any;
  }) => postDirect<RelayResult>("/relay/bet-action", params),
  relayJoinRound: (params: {
    address: string;
    bankrollId: number;
    gameId: number;
    stake: string;
    entryState?: any;
  }) => post<RelayResult>("/relay/join-round", params),
  relayArenaBetAction: (params: {
    address: string;
    betId: number;
    action: any;
  }) => post<RelayResult>("/relay/entry-action", params),
};

// --- Helpers ---

/** Convert uusdc string to human-readable USDC */
export function formatUSDC(uusdc: string): string {
  const n = Number(uusdc) / 1_000_000;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return n.toFixed(2);
}

/** Get USDC balance from BalanceResponse */
export function getUSDCBalance(balances: { denom: string; amount: string }[]): string {
  const usdc = balances.find((b) => b.denom === "uusdc");
  return usdc?.amount || "0";
}

/** Game name to display name. Falls back to engine if name is empty. */
export function gameDisplayName(name: string, engine?: string): string {
  if (name) return name.charAt(0).toUpperCase() + name.slice(1);
  switch (engine) {
    case "dice_v1": return "Dice";
    case "schrodinger_crash_v1": return "Crash";
    case "mines_v1": return "Mines";
    case "parimutuel_v1": return "Boxing";
    default: return engine || "Unknown";
  }
}

/** Engine to game type label */
export function engineTypeLabel(engine: string): string {
  switch (engine) {
    case "dice_v1": return "INSTANT";
    case "schrodinger_crash_v1": return "ARENA";
    case "mines_v1": return "STEP";
    case "parimutuel_v1": return "PARIMUTUEL";
    default: return "";
  }
}
