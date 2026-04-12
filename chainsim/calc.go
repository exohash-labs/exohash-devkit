package chainsim

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// ---------------------------------------------------------------------------
// KVStore — per-game WASM state storage
// ---------------------------------------------------------------------------

// KVStore is the interface for WASM game state persistence.
type KVStore interface {
	Get(key []byte) ([]byte, bool)
	Set(key []byte, value []byte)
	Has(key []byte) bool
	Delete(key []byte)
}

// MemKVStore is an in-memory KV store implementation.
type MemKVStore struct {
	Data map[string][]byte
}

func NewMemKVStore() *MemKVStore {
	return &MemKVStore{Data: make(map[string][]byte)}
}

func (s *MemKVStore) Get(key []byte) ([]byte, bool) {
	v, ok := s.Data[string(key)]
	return v, ok
}

func (s *MemKVStore) Set(key []byte, value []byte) {
	s.Data[string(key)] = append([]byte(nil), value...)
}

func (s *MemKVStore) Has(key []byte) bool {
	_, ok := s.Data[string(key)]
	return ok
}

func (s *MemKVStore) Delete(key []byte) {
	delete(s.Data, string(key))
}

// ---------------------------------------------------------------------------
// Context keys — pass chain + KV to host functions
// ---------------------------------------------------------------------------

type ctxKeyKVStore struct{}
type ctxKeyChain struct{}

func withKVStore(ctx context.Context, store KVStore) context.Context {
	return context.WithValue(ctx, ctxKeyKVStore{}, store)
}

func kvStoreFromCtx(ctx context.Context) KVStore {
	return ctx.Value(ctxKeyKVStore{}).(KVStore)
}

func withChain(ctx context.Context, c *Chain) context.Context {
	return context.WithValue(ctx, ctxKeyChain{}, c)
}

func chainFromCtx(ctx context.Context) *Chain {
	v := ctx.Value(ctxKeyChain{})
	if v == nil {
		return nil
	}
	return v.(*Chain)
}

// ---------------------------------------------------------------------------
// wasmRuntime — wazero with all v3 host functions registered
// ---------------------------------------------------------------------------

func newWasmRuntime(ctx context.Context) (wazero.Runtime, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler().
		WithMemoryLimitPages(8)) // 512 KB max — hard cap prevents memory.grow abuse
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	u32 := api.ValueTypeI32
	u64 := api.ValueTypeI64
	_, err := rt.NewHostModuleBuilder("env").
		// KV
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostKVGet), []api.ValueType{u32, u32}, []api.ValueType{u64}).Export("kv_get").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostKVSet), []api.ValueType{u32, u32, u32, u32}, []api.ValueType{}).Export("kv_set").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostKVHas), []api.ValueType{u32, u32}, []api.ValueType{u32}).Export("kv_has").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostKVDelete), []api.ValueType{u32, u32}, []api.ValueType{}).Export("kv_delete").
		// Scheduling
		// Financial
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostReserve), []api.ValueType{u64, u64}, []api.ValueType{u32}).Export("reserve").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostSettle), []api.ValueType{u64, u64, u32}, []api.ValueType{u32}).Export("settle").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostIncreaseStake), []api.ValueType{u64, u64}, []api.ValueType{u32}).Export("increase_stake").
		// Data
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostGetBet), []api.ValueType{u64, u32}, []api.ValueType{u32}).Export("get_bet").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostGetPendingAction), []api.ValueType{u64, u32}, []api.ValueType{u32}).Export("get_pending_action").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostGetBettor), []api.ValueType{u64, u32}, []api.ValueType{u32}).Export("get_bettor").
		// Events
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostEmitEvent), []api.ValueType{u32, u32, u32, u32}, []api.ValueType{}).Export("emit_event").
		// Gas budget
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostGetGasBudget), []api.ValueType{}, []api.ValueType{u64}).Export("get_gas_budget").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(hostGetGasUsed), []api.ValueType{}, []api.ValueType{u64}).Export("get_gas_used").
		Instantiate(ctx)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("register host functions: %w", err)
	}
	return rt, nil
}

// ---------------------------------------------------------------------------
// wasmInstance — compiled + instantiated WASM module
// ---------------------------------------------------------------------------

type wasmInstance struct {
	mod           api.Module
	fnAlloc       api.Function
	fnPlaceBet    api.Function
	fnBetAction   api.Function
	fnBlockUpdate api.Function
	fnInitGame    api.Function
	fnInfo        api.Function
	fnQuery       api.Function
	gasGlobal     api.MutableGlobal // cached gas_used exported mutable global (nil if absent)
}

func instantiateModule(ctx context.Context, rt wazero.Runtime, compiled wazero.CompiledModule) (*wasmInstance, error) {
	mod, err := rt.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}
	if initFn := mod.ExportedFunction("_initialize"); initFn != nil {
		if _, err := initFn.Call(ctx); err != nil {
			mod.Close(ctx)
			return nil, fmt.Errorf("_initialize: %w", err)
		}
	}
	inst := &wasmInstance{
		mod:           mod,
		fnAlloc:       mod.ExportedFunction("alloc"),
		fnPlaceBet:    mod.ExportedFunction("place_bet"),
		fnBetAction:   mod.ExportedFunction("bet_action"),
		fnBlockUpdate: mod.ExportedFunction("block_update"),
		fnInitGame:    mod.ExportedFunction("init_game"),
		fnInfo:        mod.ExportedFunction("info"),
		fnQuery:       mod.ExportedFunction("query"),
		gasGlobal:     toMutableGlobal(mod.ExportedGlobal("gas_used")),
	}
	if inst.fnAlloc == nil || inst.fnPlaceBet == nil || inst.fnBlockUpdate == nil {
		mod.Close(ctx)
		return nil, fmt.Errorf("missing exports: need alloc, place_bet, block_update")
	}
	return inst, nil
}

func (w *wasmInstance) close(ctx context.Context) {
	if w.mod != nil {
		w.mod.Close(ctx)
	}
}

func (w *wasmInstance) callPlaceBet(ctx context.Context, betID, bankrollID, calcID, stake uint64, params []byte) (uint32, error) {
	var ptr, length uint32
	if len(params) > 0 {
		res, err := w.fnAlloc.Call(ctx, uint64(len(params)))
		if err != nil {
			return 1, err
		}
		ptr = uint32(res[0])
		length = uint32(len(params))
		w.mod.Memory().Write(ptr, params)
	}
	res, err := w.fnPlaceBet.Call(ctx, betID, bankrollID, calcID, stake, uint64(ptr), uint64(length))
	if err != nil {
		return 1, err
	}
	return uint32(res[0]), nil
}

func (w *wasmInstance) callBetAction(ctx context.Context, betID uint64, action []byte) (uint32, error) {
	if w.fnBetAction == nil {
		return 1, fmt.Errorf("no bet_action export")
	}
	var ptr, length uint32
	if len(action) > 0 {
		res, err := w.fnAlloc.Call(ctx, uint64(len(action)))
		if err != nil {
			return 1, err
		}
		ptr = uint32(res[0])
		length = uint32(len(action))
		w.mod.Memory().Write(ptr, action)
	}
	res, err := w.fnBetAction.Call(ctx, betID, uint64(ptr), uint64(length))
	if err != nil {
		return 1, err
	}
	return uint32(res[0]), nil
}

func (w *wasmInstance) callBlockUpdate(ctx context.Context, seed []byte) error {
	if len(seed) > 0 {
		// Write seed to WASM memory via alloc.
		results, err := w.fnAlloc.Call(ctx, uint64(len(seed)))
		if err != nil {
			return err
		}
		seedPtr := uint32(results[0])
		w.mod.Memory().Write(seedPtr, seed)
		_, err = w.fnBlockUpdate.Call(ctx, uint64(seedPtr))
		return err
	}
	_, err := w.fnBlockUpdate.Call(ctx, 0)
	return err
}

func (w *wasmInstance) callInitGame(ctx context.Context, sentinelID, bankrollID, calcID uint64) error {
	if w.fnInitGame == nil {
		return nil
	}
	_, err := w.fnInitGame.Call(ctx, sentinelID, bankrollID, calcID)
	return err
}

func (w *wasmInstance) callInfo(ctx context.Context) ([]byte, error) {
	if w.fnInfo == nil {
		return nil, nil
	}
	res, err := w.fnInfo.Call(ctx)
	if err != nil {
		return nil, err
	}
	ptr := uint32(res[0])
	lenBz, _ := w.mod.Memory().Read(ptr, 4)
	respLen := binary.LittleEndian.Uint32(lenBz)
	data, _ := w.mod.Memory().Read(ptr+4, respLen)
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

func (w *wasmInstance) callQuery(ctx context.Context) ([]byte, error) {
	if w.fnQuery == nil {
		return nil, nil
	}
	res, err := w.fnQuery.Call(ctx)
	if err != nil {
		return nil, err
	}
	ptr := uint32(res[0])
	lenBz, _ := w.mod.Memory().Read(ptr, 4)
	respLen := binary.LittleEndian.Uint32(lenBz)
	data, _ := w.mod.Memory().Read(ptr+4, respLen)
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}

// ---------------------------------------------------------------------------
// Host function implementations — called by WASM, chain lock already held
// ---------------------------------------------------------------------------

func hostKVGet(ctx context.Context, mod api.Module, stack []uint64) {
	if c := chainFromCtx(ctx); c != nil { c.chargeGas(1000) }
	keyPtr, keyLen := uint32(stack[0]), uint32(stack[1])
	mem := mod.Memory()
	keyBytes, ok := mem.Read(keyPtr, keyLen)
	if !ok {
		stack[0] = 0
		return
	}
	store := kvStoreFromCtx(ctx)
	val, found := store.Get(keyBytes)
	if !found || len(val) == 0 {
		stack[0] = 0
		return
	}
	fnAlloc := mod.ExportedFunction("alloc")
	if fnAlloc == nil {
		stack[0] = 0
		return
	}
	results, err := fnAlloc.Call(ctx, uint64(len(val)))
	if err != nil {
		stack[0] = 0
		return
	}
	valPtr := uint32(results[0])
	mem.Write(valPtr, val)
	stack[0] = (uint64(valPtr) << 32) | uint64(len(val))
}

func hostKVSet(ctx context.Context, mod api.Module, stack []uint64) {
	if c := chainFromCtx(ctx); c != nil { c.chargeGas(5000) }
	keyPtr, keyLen := uint32(stack[0]), uint32(stack[1])
	valPtr, valLen := uint32(stack[2]), uint32(stack[3])
	mem := mod.Memory()
	keyBytes, _ := mem.Read(keyPtr, keyLen)
	valBytes, _ := mem.Read(valPtr, valLen)

	store := kvStoreFromCtx(ctx)
	c := chainFromCtx(ctx)
	if c != nil {
		writeBytes := uint64(len(keyBytes) + len(valBytes))
		var oldBytes uint64
		if old, ok := store.Get(keyBytes); ok {
			oldBytes = uint64(len(keyBytes) + len(old))
		}
		calcID := c.activeCalcID
		newUsage := c.kvUsage[calcID] - oldBytes + writeBytes
		budget := c.params.MaxKVBytesPerCalculator
		if budget > 0 && newUsage > budget {
			_ = c.killCalculatorLocked(calcID, "kv_budget_exceeded")
			return
		}
		c.kvUsage[calcID] = newUsage
	}
	store.Set(keyBytes, valBytes)
}

func hostKVHas(ctx context.Context, mod api.Module, stack []uint64) {
	if c := chainFromCtx(ctx); c != nil { c.chargeGas(500) }
	keyPtr, keyLen := uint32(stack[0]), uint32(stack[1])
	keyBytes, _ := mod.Memory().Read(keyPtr, keyLen)
	if kvStoreFromCtx(ctx).Has(keyBytes) {
		stack[0] = 1
	} else {
		stack[0] = 0
	}
}

func hostKVDelete(ctx context.Context, mod api.Module, stack []uint64) {
	if c := chainFromCtx(ctx); c != nil { c.chargeGas(1000) }
	keyPtr, keyLen := uint32(stack[0]), uint32(stack[1])
	keyBytes, _ := mod.Memory().Read(keyPtr, keyLen)

	store := kvStoreFromCtx(ctx)
	c := chainFromCtx(ctx)
	if c != nil {
		// Decrement usage before deleting.
		if old, ok := store.Get(keyBytes); ok {
			oldBytes := uint64(len(keyBytes) + len(old))
			calcID := c.activeCalcID
			if usage := c.kvUsage[calcID]; usage >= oldBytes {
				c.kvUsage[calcID] = usage - oldBytes
			} else {
				c.kvUsage[calcID] = 0
			}
		}
	}
	store.Delete(keyBytes)
}

func hostReserve(ctx context.Context, _ api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 1
		return
	}
	c.chargeGas(10000)
	if err := c.reserveLocked(stack[0], stack[1]); err != nil {
		stack[0] = 1
		return
	}
	stack[0] = 0
}

func hostSettle(ctx context.Context, _ api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 1
		return
	}
	c.chargeGas(10000)
	if err := c.settleLocked(stack[0], stack[1], uint8(stack[2])); err != nil {
		stack[0] = 1
		return
	}
	stack[0] = 0
}

func hostIncreaseStake(ctx context.Context, _ api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 1
		return
	}
	c.chargeGas(10000)
	if err := c.increaseStakeLocked(stack[0], stack[1]); err != nil {
		stack[0] = 1
		return
	}
	stack[0] = 0
}

func hostGetBet(ctx context.Context, mod api.Module, stack []uint64) {
	if c := chainFromCtx(ctx); c != nil { c.chargeGas(500) }
	// Not implemented in simulator — returns 0.
	stack[0] = 0
}

func hostGetPendingAction(ctx context.Context, mod api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 0
		return
	}
	c.chargeGas(500)
	data := c.getPendingActionLocked(stack[0])
	if data == nil {
		stack[0] = 0
		return
	}
	outPtr := uint32(stack[1])
	if !mod.Memory().Write(outPtr, data) {
		stack[0] = 0
		return
	}
	stack[0] = uint64(len(data))
}

func hostGetBettor(ctx context.Context, mod api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 0
		return
	}
	c.chargeGas(500)
	addr := c.getBettorLocked(stack[0])
	if addr == "" {
		stack[0] = 0
		return
	}
	outPtr := uint32(stack[1])
	addrBytes := []byte(addr)
	if !mod.Memory().Write(outPtr, addrBytes) {
		stack[0] = 0
		return
	}
	stack[0] = uint64(len(addrBytes))
}

func hostEmitEvent(ctx context.Context, mod api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		return
	}
	c.chargeGas(500)
	mem := mod.Memory()
	topicBytes, _ := mem.Read(uint32(stack[0]), uint32(stack[1]))
	dataBytes, _ := mem.Read(uint32(stack[2]), uint32(stack[3]))
	c.emitCalcEventLocked(string(topicBytes), string(dataBytes))
}

// toMutableGlobal casts an api.Global to api.MutableGlobal, returning nil if not mutable.
func toMutableGlobal(g api.Global) api.MutableGlobal {
	if g == nil {
		return nil
	}
	mg, ok := g.(api.MutableGlobal)
	if !ok {
		return nil
	}
	return mg
}

// chargeGas writes host-function gas cost directly into the WASM gas_used global.
// Unified counter: WASM instructions + host charges in one place.
// No-op if calculator is killed.
func (c *Chain) chargeGas(cost uint64) {
	calc, ok := c.calculators[c.activeCalcID]
	if !ok || calc.Status != CalcStatusActive {
		return
	}
	game, ok := c.games[c.activeCalcID]
	if !ok || game.inst == nil || game.inst.gasGlobal == nil {
		return
	}
	game.inst.gasGlobal.Set(game.inst.gasGlobal.Get() + cost)
}

func hostGetGasBudget(ctx context.Context, _ api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 0
		return
	}
	stack[0] = c.currentGasBudget
}

func hostGetGasUsed(ctx context.Context, _ api.Module, stack []uint64) {
	c := chainFromCtx(ctx)
	if c == nil {
		stack[0] = 0
		return
	}
	stack[0] = c.totalGasUsed(c.activeCalcID)
}
