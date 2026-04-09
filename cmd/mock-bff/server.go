package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

// ---------------------------------------------------------------------------
// SSE event types — match real BFF protocol
// ---------------------------------------------------------------------------

type StreamEvent struct {
	Height       uint64        `json:"height"`
	Time         string        `json:"time,omitempty"`
	BeaconSeed   string        `json:"beaconSeed,omitempty"`
	BeaconPaused bool          `json:"beaconPaused,omitempty"`
	BetsCreated  []BetCreated  `json:"betsCreated,omitempty"`
	BetsSettled  []BetSettled  `json:"betsSettled,omitempty"`
	CalcEvents   []CalcEvent   `json:"calcEvents,omitempty"`
	SystemEvents []SystemEvent `json:"systemEvents,omitempty"`
	Connected    bool          `json:"connected,omitempty"`
	Replay       *bool         `json:"replay,omitempty"`
	Heartbeat    bool          `json:"heartbeat,omitempty"`
}

type SystemEvent struct {
	Type string            `json:"type"`
	Data map[string]string `json:"data"`
}

type BetCreated struct {
	BetID        uint64 `json:"betId"`
	BankrollID   uint64 `json:"bankrollId"`
	CalculatorID uint64 `json:"calculatorId"`
	Bettor       string `json:"bettor"`
	Stake        string `json:"stake"`
	Denom        string `json:"denom"`
}

type BetSettled struct {
	BetID      uint64 `json:"betId"`
	GameID     uint64 `json:"gameId"`
	BankrollID uint64 `json:"bankrollId"`
	Bettor     string `json:"bettor"`
	Payout     string `json:"payout"`
	PayoutKind int    `json:"payoutKind"`
	NetStake   string `json:"netStake"`
	Profit     string `json:"profit"`
	Height     int64  `json:"height"`
}

type CalcEvent struct {
	CalculatorID uint64 `json:"calculatorId"`
	Topic        string `json:"topic"`
	Data         string `json:"data"`
}

type GameInfo struct {
	CalcID      uint64                              `json:"calcId"`
	Name        string                              `json:"name"`
	Engine      string                              `json:"engine,omitempty"`
	HouseEdgeBp int                                 `json:"houseEdgeBp,omitempty"`
	Status      int                                 `json:"status"` // 0=active, 1=paused, 2=killed
	Errors      map[string]map[string]string        `json:"errors,omitempty"`
}

type SubFilter struct {
	Games   map[uint64]bool
	Address string
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

type Server struct {
	chain     *chainsim.Chain
	cfg       *Config
	gameInfos map[uint64]GameInfo

	mu        sync.RWMutex
	subs      map[chan *StreamEvent]*SubFilter
	globalBuf []StreamEvent
	betEvents map[uint64][]CalcEvent // betID → calc events for cold start
}

const eventBufferSize = 10_000

func NewServer(chain *chainsim.Chain, cfg *Config) *Server {
	s := &Server{
		chain:     chain,
		cfg:       cfg,
		gameInfos: make(map[uint64]GameInfo),
		subs:      make(map[chan *StreamEvent]*SubFilter),
		betEvents: make(map[uint64][]CalcEvent),
	}

	// Build game info cache.
	for _, g := range cfg.Games {
		info := GameInfo{CalcID: g.CalcID, Name: g.Name, HouseEdgeBp: int(g.HouseEdgeBp), Status: 0}
		// Try to get engine + errors from WASM info.
		if raw, err := chain.GameInfo(g.CalcID); err == nil && raw != nil {
			var meta struct {
				Engine string                            `json:"engine"`
				Errors map[string]map[string]string      `json:"errors"`
			}
			if json.Unmarshal(raw, &meta) == nil {
				info.Engine = meta.Engine
				info.Errors = meta.Errors
			}
		}
		s.gameInfos[g.CalcID] = info
	}

	return s
}

func (s *Server) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/relay/place-bet", s.handlePlaceBet)
	mux.HandleFunc("/relay/bet-action", s.handleBetAction)
	mux.HandleFunc("/relay/info", s.handleRelayInfo)
	mux.HandleFunc("/faucet/request", s.handleFaucet)
	mux.HandleFunc("/games", s.handleGames)
	mux.HandleFunc("/health", s.handleHealth)
	// Pattern-based routes.
	mux.HandleFunc("/account/", s.handleAccount)
	mux.HandleFunc("/bet/", s.handleBetState)
	mux.HandleFunc("/game/", s.handleGameInfo)
	return corsMiddleware(mux)
}

// ---------------------------------------------------------------------------
// SSE streaming
// ---------------------------------------------------------------------------

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	filter := parseSubFilter(r)
	ch := make(chan *StreamEvent, 64)

	s.mu.Lock()
	s.subs[ch] = &filter
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
	}()

	// Connected signal.
	replayTrue := true
	connected := StreamEvent{Connected: true, Height: s.chain.Height(), Replay: &replayTrue}
	data, _ := json.Marshal(connected)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Replay buffered events.
	s.mu.RLock()
	for i := range s.globalBuf {
		filtered := filterEvent(&s.globalBuf[i], &filter)
		if filtered != nil {
			data, err := json.Marshal(filtered)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	s.mu.RUnlock()

	// End of replay.
	replayFalse := false
	endReplay := StreamEvent{Replay: &replayFalse, Height: s.chain.Height()}
	data, _ = json.Marshal(endReplay)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Live event loop.
	ctx := r.Context()
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(w, "data: {\"heartbeat\":true,\"height\":%d}\n\n", s.chain.Height())
			flusher.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (s *Server) handlePlaceBet(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Address      string `json:"address"`
		BankrollID   uint64 `json:"bankrollId"`
		CalculatorID uint64 `json:"calculatorId"`
		Stake        string `json:"stake"`
		Params       []byte `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}
	stake, _ := strconv.ParseUint(req.Stake, 10, 64)

	betID, err := s.chain.PlaceBet(req.Address, req.BankrollID, req.CalculatorID, stake, req.Params)
	if err != nil {
		// Translate WASM error codes to human-readable messages.
		jsonError(w, s.translateError(req.CalculatorID, "place_bet", err), 400)
		return
	}

	// Drain and broadcast calc events from WASM.
	wasmEvents := s.chain.DrainCalcEvents()
	ev := &StreamEvent{
		Height: s.chain.Height(),
		Time:   time.Now().UTC().Format(time.RFC3339),
		BetsCreated: []BetCreated{{
			BetID:        betID,
			BankrollID:   req.BankrollID,
			CalculatorID: req.CalculatorID,
			Bettor:       req.Address,
			Stake:        fmt.Sprintf("%d", stake),
			Denom:        "uusdc",
		}},
	}
	for _, e := range wasmEvents {
		ev.CalcEvents = append(ev.CalcEvents, CalcEvent{
			CalculatorID: req.CalculatorID,
			Topic:        e.Topic,
			Data:         e.Data,
		})
	}
	s.bufferAndBroadcast(ev)

	txHash := fmt.Sprintf("MOCK_%d_%d", s.chain.Height(), betID)
	jsonResponse(w, map[string]any{"betId": betID, "txHash": txHash})
}

func (s *Server) handleBetAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Address string `json:"address"`
		BetID   uint64 `json:"betId"`
		Action  []byte `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}

	// Find game for this bet.
	bet := s.chain.GetBet(req.BetID)
	if bet == nil {
		jsonError(w, fmt.Sprintf("unknown betId %d", req.BetID), 404)
		return
	}

	err := s.chain.BetAction(req.Address, req.BetID, req.Action)
	if err != nil {
		jsonError(w, s.translateError(bet.CalculatorID, "bet_action", err), 400)
		return
	}

	// Drain and broadcast calc events from WASM.
	wasmEvents := s.chain.DrainCalcEvents()
	if len(wasmEvents) > 0 {
		ev := &StreamEvent{
			Height: s.chain.Height(),
			Time:   time.Now().UTC().Format(time.RFC3339),
		}
		for _, e := range wasmEvents {
			ev.CalcEvents = append(ev.CalcEvents, CalcEvent{
				CalculatorID: bet.CalculatorID,
				Topic:        e.Topic,
				Data:         e.Data,
			})
		}
		s.bufferAndBroadcast(ev)
	}

	txHash := fmt.Sprintf("MOCK_%d_%d", s.chain.Height(), req.BetID)
	jsonResponse(w, map[string]any{"txHash": txHash})
}

func (s *Server) handleRelayInfo(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{"enabled": true, "relayAddress": "mock-relay"})
}

func (s *Server) handleFaucet(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}

	amount := s.cfg.Faucet.Amount
	if err := s.chain.Deposit(req.Address, amount); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}

	bal, _ := s.chain.Balance(req.Address)
	log.Printf("Faucet: %s +%d uusdc", req.Address, amount)
	jsonResponse(w, map[string]any{
		"txHash":  fmt.Sprintf("FAUCET_%d", s.chain.Height()),
		"amount":  fmt.Sprintf("%d", amount),
		"balance": fmt.Sprintf("%d", bal),
	})
}

func (s *Server) handleGames(w http.ResponseWriter, r *http.Request) {
	games := make([]GameInfo, 0, len(s.gameInfos))
	for _, g := range s.gameInfos {
		games = append(games, g)
	}
	jsonResponse(w, games)
}

func (s *Server) handleGameInfo(w http.ResponseWriter, r *http.Request) {
	// /game/{id}/info
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, "invalid path", 400)
		return
	}
	calcID, _ := strconv.ParseUint(parts[1], 10, 64)
	info, ok := s.gameInfos[calcID]
	if !ok {
		jsonError(w, "game not found", 404)
		return
	}
	jsonResponse(w, info)
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	// /account/{addr}/balance or /account/{addr}/bets
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, "invalid path", 400)
		return
	}
	addr := parts[1]
	action := parts[2]

	switch action {
	case "balance":
		bal, err := s.chain.Balance(addr)
		if err != nil {
			jsonError(w, err.Error(), 404)
			return
		}
		jsonResponse(w, map[string]any{"address": addr, "usdc": fmt.Sprintf("%d", bal)})

	case "bets":
		limit := 50
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil {
				limit = n
			}
		}
		bets := s.chain.BetHistory(addr, limit)
		out := make([]map[string]any, 0, len(bets))
		for _, b := range bets {
			out = append(out, map[string]any{
				"betId":  b.ID,
				"gameId": b.CalculatorID,
				"stake":  b.Stake,
				"payout": b.Payout,
				"status": betStatusString(b),
			})
		}
		jsonResponse(w, out)

	default:
		jsonError(w, "unknown action", 400)
	}
}

func (s *Server) handleBetState(w http.ResponseWriter, r *http.Request) {
	// /bet/{id}/state
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, "invalid path", 400)
		return
	}
	betID, _ := strconv.ParseUint(parts[1], 10, 64)
	bet := s.chain.GetBet(betID)
	if bet == nil {
		jsonError(w, "bet not found", 404)
		return
	}

	s.mu.RLock()
	events := s.betEvents[betID]
	s.mu.RUnlock()

	jsonResponse(w, map[string]any{
		"betId":  bet.ID,
		"gameId": bet.CalculatorID,
		"stake":  bet.Stake,
		"payout": bet.Payout,
		"status": betStatusString(*bet),
		"events": events,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, map[string]any{
		"height": s.chain.Height(),
		"games":  len(s.cfg.Games),
		"status": "ok",
	})
}

// ---------------------------------------------------------------------------
// Block ticker
// ---------------------------------------------------------------------------

func (s *Server) blockTicker(ctx context.Context, blockTime time.Duration) {
	ticker := time.NewTicker(blockTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.advanceBlock()
		}
	}
}

func (s *Server) advanceBlock() {
	result := s.chain.AdvanceBlock()
	block := result.Block

	var calcEvents []CalcEvent
	for _, e := range result.CalcEvents {
		calcEvents = append(calcEvents, CalcEvent{
			CalculatorID: e.CalcID,
			Topic:        e.Topic,
			Data:         e.Data,
		})
	}

	var settled []BetSettled
	for _, st := range result.Settlements {
		bet := s.chain.GetBet(st.BetID)
		gameID := st.CalcID
		var bettor string
		var netStake uint64
		if bet != nil {
			if gameID == 0 {
				gameID = bet.CalculatorID
			}
			bettor = bet.Bettor
			netStake = bet.NetStake
		}
		kind := 2
		if st.Payout > 0 {
			kind = 1
		}
		if st.Kind == 3 {
			kind = 3
		}
		// Profit from bankroll perspective: stake kept minus payout.
		profit := int64(netStake) - int64(st.Payout)
		settled = append(settled, BetSettled{
			BetID:      st.BetID,
			GameID:     gameID,
			BankrollID: 1,
			Bettor:     bettor,
			Payout:     fmt.Sprintf("%d", st.Payout),
			PayoutKind: kind,
			NetStake:   fmt.Sprintf("%d", netStake),
			Profit:     fmt.Sprintf("%d", profit),
			Height:     int64(block.Height),
		})
	}

	ev := &StreamEvent{
		Height:     block.Height,
		Time:       time.Now().UTC().Format(time.RFC3339),
		CalcEvents: calcEvents,
		BetsSettled: settled,
	}

	if len(calcEvents) > 0 || len(settled) > 0 || block.Height%10 == 0 {
		s.bufferAndBroadcast(ev)
	}
}

// ---------------------------------------------------------------------------
// Broadcasting
// ---------------------------------------------------------------------------

func (s *Server) bufferAndBroadcast(ev *StreamEvent) {
	ev.BeaconSeed = hex.EncodeToString(s.chain.GetRNG(ev.Height))

	s.mu.Lock()
	s.globalBuf = append(s.globalBuf, *ev)
	if len(s.globalBuf) > eventBufferSize {
		s.globalBuf = s.globalBuf[len(s.globalBuf)-eventBufferSize:]
	}

	// Track per-bet events for cold start.
	s.trackBetEvents(ev.CalcEvents)

	for ch, filter := range s.subs {
		filtered := filterEvent(ev, filter)
		if filtered == nil {
			continue
		}
		select {
		case ch <- filtered:
		default:
		}
	}
	s.mu.Unlock()
}

func (s *Server) trackBetEvents(events []CalcEvent) {
	for _, ev := range events {
		var data struct {
			BetID uint64 `json:"bet_id"`
		}
		if json.Unmarshal([]byte(ev.Data), &data) == nil && data.BetID > 0 {
			s.betEvents[data.BetID] = append(s.betEvents[data.BetID], ev)
		}
	}
}

// ---------------------------------------------------------------------------
// Error translation
// ---------------------------------------------------------------------------

func (s *Server) translateError(calcID uint64, method string, err error) string {
	info, ok := s.gameInfos[calcID]
	if !ok || info.Errors == nil {
		return err.Error()
	}
	methods, ok := info.Errors[method]
	if !ok {
		return err.Error()
	}
	// Extract status code from error message: "... (status=N)"
	errMsg := err.Error()
	for code, msg := range methods {
		if strings.Contains(errMsg, "(status="+code+")") {
			return msg
		}
	}
	return errMsg
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func parseSubFilter(r *http.Request) SubFilter {
	var f SubFilter
	if games := r.URL.Query().Get("games"); games != "" {
		f.Games = make(map[uint64]bool)
		for _, g := range strings.Split(games, ",") {
			if id, err := strconv.ParseUint(strings.TrimSpace(g), 10, 64); err == nil {
				f.Games[id] = true
			}
		}
	}
	f.Address = r.URL.Query().Get("address")
	return f
}

func filterEvent(ev *StreamEvent, filter *SubFilter) *StreamEvent {
	out := &StreamEvent{
		Height:     ev.Height,
		Time:       ev.Time,
		BeaconSeed: ev.BeaconSeed,
	}

	for _, ce := range ev.CalcEvents {
		if filter.Games == nil || filter.Games[ce.CalculatorID] {
			out.CalcEvents = append(out.CalcEvents, ce)
		}
	}

	for _, bc := range ev.BetsCreated {
		if filter.Address == "" || bc.Bettor == filter.Address {
			out.BetsCreated = append(out.BetsCreated, bc)
		}
	}
	out.BetsSettled = ev.BetsSettled

	if len(out.CalcEvents) == 0 && len(out.BetsCreated) == 0 && len(out.BetsSettled) == 0 {
		return nil
	}
	return out
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonResponse(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"error": msg, "code": code})
}

// betStatusString maps chainsim bet status to real BFF status strings.
func betStatusString(b chainsim.Bet) string {
	switch b.Status {
	case chainsim.BetOpen:
		return "open"
	case chainsim.BetSettled:
		if b.Payout > 0 {
			return "win"
		}
		return "loss"
	case chainsim.BetRefunded:
		return "refund"
	default:
		return "open"
	}
}
