package chainsim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero"
)

// gameState holds a compiled WASM game module and its persistent instance.
type gameState struct {
	calcID    uint64
	compiled  interface{} // wazero.CompiledModule (stored as interface to avoid nil type issues)
	inst      *wasmInstance
	kv        *MemKVStore
	allocCount uint64 // track alloc calls for periodic re-instantiation
}

// RegisterGame compiles a WASM calculator and registers it with the chain.
// After registration, PlaceBet/BetAction/AdvanceBlock will invoke the WASM automatically.
func (c *Chain) RegisterGame(calcID uint64, wasmBytes []byte, name string, houseEdgeBp uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.wasmRT == nil {
		return fmt.Errorf("WASM runtime not initialized")
	}

	ctx := context.Background()

	// Compile the module.
	compiled, err := c.wasmRT.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("compile %s: %w", name, err)
	}

	// Create KV store for this game.
	kv := NewMemKVStore()

	// Instantiate persistent module.
	wasmCtx := withChain(withKVStore(ctx, kv), c)
	inst, err := instantiateModule(wasmCtx, c.wasmRT, compiled)
	if err != nil {
		return fmt.Errorf("instantiate %s: %w", name, err)
	}

	// Extract metadata from info() export.
	engine := "unknown"
	edgeBp := houseEdgeBp
	gameName := name
	info, err := inst.callInfo(ctx)
	if err == nil && info != nil {
		var meta struct {
			Name        string `json:"name"`
			Engine      string `json:"engine"`
			HouseEdgeBp uint64 `json:"house_edge_bp"`
		}
		if json.Unmarshal(info, &meta) == nil {
			if meta.Name != "" {
				gameName = meta.Name
			}
			if meta.Engine != "" {
				engine = meta.Engine
			}
			if meta.HouseEdgeBp > 0 {
				edgeBp = meta.HouseEdgeBp
			}
		}
	}

	// Register calculator in chain state.
	calc := Calculator{
		ID:          calcID,
		Name:        gameName,
		Engine:      engine,
		HouseEdgeBp: edgeBp,
		Status:      CalcStatusActive,
	}
	// Remove existing calculator if re-registering.
	delete(c.calculators, calcID)
	c.calculators[calcID] = &calc

	// Store game state.
	c.games[calcID] = &gameState{
		calcID:   calcID,
		compiled: compiled,
		inst:     inst,
		kv:       kv,
	}

	return nil
}

// InitGame calls the WASM init_game export for a registered game.
// Creates a sentinel bet to kickstart the calculator (mirrors keeper).
func (c *Chain) InitGame(calcID uint64, bankrollID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	game, ok := c.games[calcID]
	if !ok {
		return fmt.Errorf("game %d not registered", calcID)
	}

	// Find bankroll creator for sentinel bet address.
	sentinelAddr := "sentinel"
	if br, ok := c.bankrolls[bankrollID]; ok {
		sentinelAddr = br.Creator
	}

	// Create sentinel bet (stake=0, no balance check needed).
	c.mode = CalcModePlaceBet
	sentinelID := c.nextBetID
	c.nextBetID++
	c.bets[sentinelID] = &Bet{
		ID:           sentinelID,
		BankrollID:   bankrollID,
		CalculatorID: calcID,
		Bettor:       sentinelAddr,
		Status:       BetOpen,
	}
	c.betGame[sentinelID] = calcID

	// Call WASM init_game.
	c.mode = CalcModeInitGame
	c.activeCalcID = calcID
	ctx := withChain(withKVStore(context.Background(), game.kv), c)
	return game.inst.callInitGame(ctx, sentinelID, bankrollID, calcID)
}

// GameInfo returns the raw JSON from the WASM info() export.
func (c *Chain) GameInfo(calcID uint64) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	game, ok := c.games[calcID]
	if !ok {
		return nil, fmt.Errorf("game %d not registered", calcID)
	}
	ctx := context.Background()
	return game.inst.callInfo(ctx)
}

// GameQuery calls the WASM query() export and returns game-specific state JSON.
func (c *Chain) GameQuery(calcID uint64) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	game, ok := c.games[calcID]
	if !ok {
		return nil, fmt.Errorf("game %d not registered", calcID)
	}
	ctx := withChain(withKVStore(context.Background(), game.kv), c)
	return game.inst.callQuery(ctx)
}

// wasmCtxForGame creates a context with chain and KV store for a game.
// Caller must hold c.mu.
func (c *Chain) wasmCtxForGame(calcID uint64) (context.Context, *gameState, error) {
	game, ok := c.games[calcID]
	if !ok {
		return nil, nil, fmt.Errorf("game %d not registered", calcID)
	}
	ctx := withChain(withKVStore(context.Background(), game.kv), c)
	return ctx, game, nil
}

// reinstantiateThreshold controls how many alloc calls before we recycle the
// WASM instance to reclaim linear memory. Wazero's linear memory only grows,
// never shrinks within a single module instance.
const reinstantiateThreshold = 5000

// reinstantiateIfNeeded closes the current WASM instance and creates a fresh
// one from the compiled module, preserving KV state. Caller must hold c.mu.
func (c *Chain) reinstantiateIfNeeded(calcID uint64) {
	game, ok := c.games[calcID]
	if !ok {
		return
	}
	game.allocCount++
	if game.allocCount < reinstantiateThreshold {
		return
	}
	game.allocCount = 0

	ctx := withChain(withKVStore(context.Background(), game.kv), c)
	compiled, ok := game.compiled.(wazero.CompiledModule)
	if !ok {
		return
	}

	// Close old instance.
	game.inst.close(ctx)

	// Create fresh instance — KV store is external, so state is preserved.
	inst, err := instantiateModule(ctx, c.wasmRT, compiled)
	if err != nil {
		fmt.Printf("reinstantiate error (calc=%d): %v\n", calcID, err)
		return
	}
	game.inst = inst
}
