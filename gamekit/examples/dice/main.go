// Dice calculator v3 — host-callback protocol.
//
// Instead of receiving binary messages and returning commands,
// this calculator calls host functions directly:
//   schedule_wakeup, reserve, settle, get_rng, emit_event, etc.
//
// Exports: alloc, dealloc, place_bet, block_update, info
// Imports: env.kv_get, env.kv_set, env.kv_has,
//          env.schedule_wakeup, env.reserve, env.settle,
//          env.get_rng, env.get_bet_count, env.get_bet_id, env.get_bet,
//          env.emit_event
package main

import (
	"encoding/binary"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Host imports
// ---------------------------------------------------------------------------

//go:wasmimport env kv_get
func kv_get(keyPtr, keyLen uint32) uint64

//go:wasmimport env kv_set
func kv_set(keyPtr, keyLen, valPtr, valLen uint32)

//go:wasmimport env kv_has
func kv_has(keyPtr, keyLen uint32) uint32

//go:wasmimport env schedule_wakeup
func schedule_wakeup(betID, height uint64)

//go:wasmimport env reserve
func host_reserve(betID, amount uint64) uint32

//go:wasmimport env settle
func host_settle(betID, payout uint64, kind uint32) uint32

//go:wasmimport env get_rng
func host_get_rng(height uint64, outPtr uint32) uint32

//go:wasmimport env get_bet_count
func host_get_bet_count() uint32

//go:wasmimport env get_bet_id
func host_get_bet_id(index uint32) uint64

//go:wasmimport env get_bet
func host_get_bet(betID uint64, outPtr uint32) uint32

//go:wasmimport env emit_event
func host_emit_event(topicPtr, topicLen, dataPtr, dataLen uint32)

// ---------------------------------------------------------------------------
// Memory management
// ---------------------------------------------------------------------------

//export alloc
func alloc(size uint32) *byte {
	buf := make([]byte, size)
	return &buf[0]
}

//export dealloc
func dealloc(ptr *byte, size uint32) {}

// ---------------------------------------------------------------------------
// Game constants
// ---------------------------------------------------------------------------

const (
	houseEdgeBP = 200
	minChanceBP = 100
	maxChanceBP = 9800

	kindWin  = 1
	kindLoss = 2
)

// ---------------------------------------------------------------------------
// place_bet — called during MsgPlaceBet tx
// ---------------------------------------------------------------------------

//export place_bet
func place_bet(betID, bankrollID, calculatorID, stake uint64, paramsPtr, paramsLen uint32) uint32 {
	// Params layout: sender(20) + mode(1) + threshold(8) = 29 bytes.
	params := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(paramsPtr))), paramsLen)
	if len(params) < 29 {
		return 1 // invalid params
	}
	// sender := params[0:20] — not used by dice
	betMode := params[20]
	threshold := binary.LittleEndian.Uint64(params[21:29])

	chance := chanceBP(betMode, threshold)
	if chance < minChanceBP || chance > maxChanceBP || chance == 0 {
		return 2 // chance out of range
	}

	maxPayout := stake * fairMultBP(chance) / 10000

	// Reserve bankroll liquidity.
	if host_reserve(betID, maxPayout) != 0 {
		return 3 // insufficient liquidity
	}

	// Store bet state in KV for block_update to use.
	// 33 bytes: betID(8) + stake(8) + mode(1) + threshold(8) + rngHeight(8)
	state := make([]byte, 33)
	binary.LittleEndian.PutUint64(state[0:], betID)
	binary.LittleEndian.PutUint64(state[8:], stake)
	state[16] = betMode
	binary.LittleEndian.PutUint64(state[17:], threshold)
	// RNG height = current block height (keeper provides RNG for this height at next block)
	// We don't have height directly, but schedule_wakeup(betID, 0) means "next block".
	// Store a sentinel — block_update will use height-1 for RNG.
	binary.LittleEndian.PutUint64(state[25:], 0) // filled by block_update
	kvSet(betKey(betID), state)

	// Get current block height from a simple trick: we know height from context.
	// Actually, we need height for schedule_wakeup. The keeper passes it implicitly.
	// For v3, use the betID's KV to find height. Simpler: the keeper already knows
	// the height — WASM just says "wake me up at next block".
	// Use height=0 as sentinel for "current height + 1".
	schedule_wakeup(betID, 0) // 0 = keeper interprets as currentHeight+1

	// Emit bet event.
	emitJSON("bet", "entry_id", betID, "stake", stake, "chance_bp", chance, "max_payout", maxPayout)

	return 0 // success
}

// ---------------------------------------------------------------------------
// block_update — called once per block in BeginBlock
// ---------------------------------------------------------------------------

//export block_update
func block_update(height uint64) {
	count := host_get_bet_count()
	for i := uint32(0); i < count; i++ {
		betID := host_get_bet_id(i)
		settleBet(betID, height)
	}
}

func settleBet(betID, height uint64) {
	// Load bet state from KV.
	state := kvGetBytes(betKey(betID))
	if state == nil || len(state) < 25 {
		return
	}

	storedBetID := binary.LittleEndian.Uint64(state[0:8])
	stake := binary.LittleEndian.Uint64(state[8:16])
	betMode := state[16]
	threshold := binary.LittleEndian.Uint64(state[17:25])

	// RNG height = block before this one (bet placed at height-1, RNG available now).
	rngHeight := height - 1
	rngBuf := make([]byte, 32)
	ok := host_get_rng(rngHeight, uint32(uintptr(unsafe.Pointer(&rngBuf[0]))))
	if ok == 0 {
		// RNG not available — reschedule.
		schedule_wakeup(betID, height+1)
		return
	}

	// Derive roll.
	chance := chanceBP(betMode, threshold)
	mult := fairMultBP(chance)
	effChance := chance * (10000 - houseEdgeBP) / 10000
	roll := deriveRoll(rngBuf, storedBetID)
	win := isWin(betMode, roll, effChance)

	payout := uint64(0)
	settleKind := uint32(kindLoss)
	if win {
		payout = stake * mult / 10000
		settleKind = uint32(kindWin)
	}

	// Settle via host.
	host_settle(betID, payout, settleKind)

	// Emit settle event.
	resultStr := "loss"
	if win {
		resultStr = "win"
	}
	emitSettleJSON(storedBetID, roll, chance, effChance, mult, payout, resultStr)
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

//export info
func info() *byte {
	data := []byte(`{"name":"Dice v3","engine":"dice","mode":"v3","house_edge_bp":200,"developer":"ExoHash","description":"Provably fair dice — host-callback protocol"}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
}

// ---------------------------------------------------------------------------
// KV helpers
// ---------------------------------------------------------------------------

func betKey(betID uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'b'
	binary.LittleEndian.PutUint64(buf[1:], betID)
	return buf
}

func kvSet(key, value []byte) {
	kv_set(
		uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)),
		uint32(uintptr(unsafe.Pointer(&value[0]))), uint32(len(value)),
	)
}

func kvGetBytes(key []byte) []byte {
	packed := kv_get(uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)))
	if packed == 0 {
		return nil
	}
	ptr := uint32(packed >> 32)
	length := uint32(packed & 0xFFFFFFFF)
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

// ---------------------------------------------------------------------------
// Dice math
// ---------------------------------------------------------------------------

func chanceBP(mode byte, threshold uint64) uint64 {
	switch mode {
	case 1:
		return 10000 - threshold
	case 2:
		return threshold
	default:
		return 0
	}
}

func fairMultBP(chance uint64) uint64 {
	if chance == 0 {
		return 0
	}
	return (10000 * 10000) / chance
}

func isWin(mode byte, roll, effChance uint64) bool {
	switch mode {
	case 1:
		return roll >= (10000 - effChance)
	case 2:
		return roll < effChance
	default:
		return false
	}
}

func deriveRoll(seed []byte, entryID uint64) uint64 {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], entryID)
	data := make([]byte, len(seed)+8)
	copy(data, seed)
	copy(data[len(seed):], buf[:])
	sum := sha256sum(data)
	return binary.BigEndian.Uint64(sum[0:8]) % 10000
}

// ---------------------------------------------------------------------------
// Event helpers
// ---------------------------------------------------------------------------

func emitJSON(topic string, pairs ...interface{}) {
	json := fmtJSON(pairs...)
	topicBytes := []byte(topic)
	jsonBytes := []byte(json)
	host_emit_event(
		uint32(uintptr(unsafe.Pointer(&topicBytes[0]))), uint32(len(topicBytes)),
		uint32(uintptr(unsafe.Pointer(&jsonBytes[0]))), uint32(len(jsonBytes)),
	)
}

func emitSettleJSON(entryID, roll, chance, effChance, mult, payout uint64, result string) {
	json := fmtJSON("entry_id", entryID, "roll", roll, "chance_bp", chance, "eff_chance_bp", effChance, "mult_bp", mult, "payout", payout, "result", result)
	topic := []byte("settle")
	jsonBytes := []byte(json)
	host_emit_event(
		uint32(uintptr(unsafe.Pointer(&topic[0]))), uint32(len(topic)),
		uint32(uintptr(unsafe.Pointer(&jsonBytes[0]))), uint32(len(jsonBytes)),
	)
}

func fmtJSON(pairs ...interface{}) string {
	buf := make([]byte, 0, 128)
	buf = append(buf, '{')
	for i := 0; i < len(pairs)-1; i += 2 {
		if i > 0 {
			buf = append(buf, ',')
		}
		key := pairs[i].(string)
		buf = append(buf, '"')
		buf = append(buf, key...)
		buf = append(buf, '"', ':')
		switch v := pairs[i+1].(type) {
		case uint64:
			buf = appendUint(buf, v)
		case string:
			buf = append(buf, '"')
			buf = append(buf, v...)
			buf = append(buf, '"')
		}
	}
	buf = append(buf, '}')
	return string(buf)
}

func appendUint(buf []byte, v uint64) []byte {
	if v == 0 {
		return append(buf, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for v > 0 {
		i--
		tmp[i] = byte('0' + v%10)
		v /= 10
	}
	return append(buf, tmp[i:]...)
}

// ---------------------------------------------------------------------------
// SHA-256 (no crypto/sha256 — FIPS panic in WASM)
// ---------------------------------------------------------------------------

var sha256K = [64]uint32{
	0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
	0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
	0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
	0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
	0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
	0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
	0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
	0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
}

func sha256sum(data []byte) [32]byte {
	h0 := uint32(0x6a09e667)
	h1 := uint32(0xbb67ae85)
	h2 := uint32(0x3c6ef372)
	h3 := uint32(0xa54ff53a)
	h4 := uint32(0x510e527f)
	h5 := uint32(0x9b05688c)
	h6 := uint32(0x1f83d9ab)
	h7 := uint32(0x5be0cd19)

	msgLen := len(data)
	bitLen := uint64(msgLen) * 8
	data = append(data, 0x80)
	for len(data)%64 != 56 {
		data = append(data, 0)
	}
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], bitLen)
	data = append(data, lenBuf[:]...)

	var w [64]uint32
	for off := 0; off < len(data); off += 64 {
		block := data[off : off+64]
		for i := 0; i < 16; i++ {
			w[i] = binary.BigEndian.Uint32(block[i*4:])
		}
		for i := 16; i < 64; i++ {
			s0 := rotr32(w[i-15], 7) ^ rotr32(w[i-15], 18) ^ (w[i-15] >> 3)
			s1 := rotr32(w[i-2], 17) ^ rotr32(w[i-2], 19) ^ (w[i-2] >> 10)
			w[i] = w[i-16] + s0 + w[i-7] + s1
		}

		a, b, c, d, e, f, g, h := h0, h1, h2, h3, h4, h5, h6, h7
		for i := 0; i < 64; i++ {
			S1 := rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25)
			ch := (e & f) ^ (^e & g)
			temp1 := h + S1 + ch + sha256K[i] + w[i]
			S0 := rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22)
			maj := (a & b) ^ (a & c) ^ (b & c)
			temp2 := S0 + maj
			h = g; g = f; f = e; e = d + temp1
			d = c; c = b; b = a; a = temp1 + temp2
		}
		h0 += a; h1 += b; h2 += c; h3 += d
		h4 += e; h5 += f; h6 += g; h7 += h
	}

	var out [32]byte
	binary.BigEndian.PutUint32(out[0:], h0)
	binary.BigEndian.PutUint32(out[4:], h1)
	binary.BigEndian.PutUint32(out[8:], h2)
	binary.BigEndian.PutUint32(out[12:], h3)
	binary.BigEndian.PutUint32(out[16:], h4)
	binary.BigEndian.PutUint32(out[20:], h5)
	binary.BigEndian.PutUint32(out[24:], h6)
	binary.BigEndian.PutUint32(out[28:], h7)
	return out
}

func rotr32(x uint32, n uint) uint32 {
	return (x >> n) | (x << (32 - n))
}

func main() {}
