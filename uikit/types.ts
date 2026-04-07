// ---------------------------------------------------------------------------
// SSE event types — match mock-bff/types.go exactly
// ---------------------------------------------------------------------------

export interface StreamEvent {
  height: number;
  time: string;
  beaconSeed?: string;
  betsCreated?: BetCreated[];
  betsSettled?: BetSettled[];
  calcEvents?: CalcEvent[];
  // Control events
  connected?: boolean;
  replay?: boolean;
  heartbeat?: boolean;
}

export interface BetCreated {
  betId: number;
  bankrollId: number;
  bettor: string;
  stake: string;
  denom: string;
}

export interface BetSettled {
  betId: number;
  gameId: number;
  bankrollId: number;
  payout: string;
  payoutKind: number; // 1=win, 2=loss, 3=refund
  height: number;
}

export interface CalcEvent {
  calculatorId: number;
  topic: string;
  data: string; // raw JSON
}

// ---------------------------------------------------------------------------
// REST response types
// ---------------------------------------------------------------------------

export interface GameInfo {
  calcId: number;
  name: string;
  engine?: string;
  houseEdgeBp?: number;
  errors?: Record<string, Record<string, string>>; // method → code → message
}

export interface PlaceBetResponse {
  betId: number;
  txHash: string;
}

export interface BetActionResponse {
  txHash: string;
}

export interface FaucetResponse {
  txHash: string;
  amount: string;
  balance: string;
}

export interface BalanceResponse {
  address: string;
  usdc: string;
}

export interface BetRecord {
  betId: number;
  gameId: number;
  stake: number;
  payout: number;
  status: string;
}

export interface BetState {
  betId: number;
  gameId: number;
  stake: number;
  payout: number;
  status: string;
  events: CalcEvent[];
}

export interface HealthResponse {
  height: number;
  games: number;
  status: string;
}

// ---------------------------------------------------------------------------
// Game-specific event data (parsed from CalcEvent.data JSON)
// ---------------------------------------------------------------------------

// Crash — matches WASM v3 event protocol
export interface CrashStateEvent { phase: string; round: number; mult_bp: number; tick: number; blocks_left: number; players: number; active: number; cashed: number; stake: number }
export interface CrashJoinedEvent { bet_id: number; addr: string; stake: number; players: number }
export interface CrashCashoutEvent { bet_id: number; addr: string; mult_bp: number; payout: number }
export interface CrashSettledEvent { bet_id: number; addr: string; payout: number; kind: number }

// Dice
export interface DiceBetEvent { entry_id: number; stake: number; chance_bp: number; max_payout: number }

// Mines
export interface MinesInitEvent { table_size: number }
export interface MinesBetEvent { entry_id: number; stake: number; mines: number; max_payout: number }
export interface MinesRevealPendingEvent { entry_id: number; tile: number }
export interface MinesTileSafeEvent { entry_id: number; tile: number; revealed: number; mult_bp: number; next_payout?: number; payout?: number; auto_cashout?: number }
export interface MinesMineHitEvent { entry_id: number; tile: number; mines: number; revealed: number }
export interface MinesCashoutEvent { entry_id: number; revealed: number; mult_bp: number; payout: number }

// ---------------------------------------------------------------------------
// API error response
// ---------------------------------------------------------------------------

export interface ApiError {
  error: string;
  code?: number;
}
