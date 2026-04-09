package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/exohash-labs/exohash-devkit/chainsim"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Initialize chain simulator.
	params := chainsim.DefaultParams()
	if cfg.Chain.MaxKVBytesPerCalculator > 0 {
		params.MaxKVBytesPerCalculator = cfg.Chain.MaxKVBytesPerCalculator
	}
	if cfg.Chain.MinStakeUusdc > 0 {
		params.MinStakeUusdc = cfg.Chain.MinStakeUusdc
	}
	chain := chainsim.NewWithParams(params, cfg.Seed)
	defer chain.Close()

	// Set up LP + bankroll.
	lp := "house"
	if err := chain.Deposit(lp, cfg.Bankroll.Deposit); err != nil {
		log.Fatalf("deposit lp: %v", err)
	}
	brID, err := chain.CreateBankroll(lp, cfg.Bankroll.Deposit, cfg.Bankroll.Name, false)
	if err != nil {
		log.Fatalf("create bankroll: %v", err)
	}

	// Register games.
	for _, g := range cfg.Games {
		wasmBytes, err := os.ReadFile(g.Wasm)
		if err != nil {
			log.Fatalf("read %s: %v", g.Wasm, err)
		}
		if err := chain.RegisterGame(g.CalcID, wasmBytes, g.Name, g.HouseEdgeBp); err != nil {
			log.Fatalf("register %s: %v", g.Name, err)
		}
		if err := chain.AttachGame(brID, g.CalcID); err != nil {
			log.Fatalf("attach %s: %v", g.Name, err)
		}
		if err := chain.InitGame(g.CalcID, brID); err != nil {
			log.Fatalf("init %s: %v", g.Name, err)
		}
		log.Printf("Loaded game: %s (calcID=%d)", g.Name, g.CalcID)
	}

	log.Printf("Bankroll #%d: %s, %d USDC, %d games",
		brID, cfg.Bankroll.Name, cfg.Bankroll.Deposit/1_000_000, len(cfg.Games))

	// Create server.
	srv := NewServer(chain, cfg)

	// Block ticker.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	blockTime := time.Duration(cfg.BlockTimeMs) * time.Millisecond
	go srv.blockTicker(ctx, blockTime)

	// HTTP server.
	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("Mock BFF on http://localhost%s", addr)
	log.Printf("  SSE:      GET  /stream?games=1,2&address=x")
	log.Printf("  Relay:    POST /relay/place-bet, /relay/bet-action")
	log.Printf("  Faucet:   POST /faucet/request")
	log.Printf("  Account:  GET  /account/{addr}/balance, /account/{addr}/bets")
	log.Printf("  Bet:      GET  /bet/{id}/state")
	log.Printf("  Games:    GET  /games, /game/{id}/info")
	log.Printf("  Health:   GET  /health")

	httpSrv := &http.Server{Addr: addr, Handler: srv.mux()}
	go func() {
		<-ctx.Done()
		httpSrv.Close()
	}()

	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
