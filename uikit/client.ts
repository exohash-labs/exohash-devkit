import type {
  GameInfo,
  PlaceBetResponse,
  BetActionResponse,
  FaucetResponse,
  BalanceResponse,
  BetRecord,
  BetState,
  HealthResponse,
} from "./types";

// ---------------------------------------------------------------------------
// ExoClient — REST client for BFF relay endpoints
// ---------------------------------------------------------------------------

export class ExoClient {
  constructor(private baseUrl: string) {}

  private async post<T>(path: string, body: unknown): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json();
    if (!res.ok || data.error) {
      throw new ExoApiError(data.error || `HTTP ${res.status}`, data.code);
    }
    return data as T;
  }

  private async get<T>(path: string): Promise<T> {
    const res = await fetch(`${this.baseUrl}${path}`);
    const data = await res.json();
    if (!res.ok || data.error) {
      throw new ExoApiError(data.error || `HTTP ${res.status}`, data.code);
    }
    return data as T;
  }

  // --- Relay ---

  async placeBet(params: {
    address: string;
    bankrollId: number;
    calculatorId: number;
    stake: string;
    params: number[];
  }): Promise<PlaceBetResponse> {
    return this.post("/relay/place-bet", params);
  }

  async betAction(params: {
    address: string;
    betId: number;
    action: number[];
  }): Promise<BetActionResponse> {
    return this.post("/relay/bet-action", params);
  }

  async relayInfo(): Promise<{ enabled: boolean; relayAddress: string }> {
    return this.get("/relay/info");
  }

  // --- Faucet ---

  async faucet(address: string): Promise<FaucetResponse> {
    return this.post("/faucet/request", { address });
  }

  // --- Account ---

  async balance(address: string): Promise<BalanceResponse> {
    return this.get(`/account/${address}/balance`);
  }

  async bets(address: string, limit = 50): Promise<BetRecord[]> {
    return this.get(`/account/${address}/bets?limit=${limit}`);
  }

  // --- Bet state (cold start) ---

  async betState(betId: number): Promise<BetState> {
    return this.get(`/bet/${betId}/state`);
  }

  // --- Games ---

  async games(): Promise<GameInfo[]> {
    return this.get("/games");
  }

  async gameInfo(calcId: number): Promise<GameInfo> {
    return this.get(`/game/${calcId}/info`);
  }

  // --- Health ---

  async health(): Promise<HealthResponse> {
    return this.get("/health");
  }
}

// ---------------------------------------------------------------------------
// Error type
// ---------------------------------------------------------------------------

export class ExoApiError extends Error {
  constructor(message: string, public code?: number) {
    super(message);
    this.name = "ExoApiError";
  }
}
