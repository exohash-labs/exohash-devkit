// Mines calculator v3 — host-callback protocol.
//
// Instead of receiving binary messages and returning commands,
// this calculator calls host functions directly:
//   schedule_wakeup, reserve, settle, get_rng, emit_event, etc.
//
// Exports: alloc, dealloc, place_bet, bet_action, block_update, info
// Imports: env.kv_get, env.kv_set,
//          env.schedule_wakeup, env.reserve, env.settle,
//          env.get_rng, env.get_bet_count, env.get_bet_id,
//          env.emit_event
//
// Security model:
//   Each tile reveal requires a fresh RNG seed from the beacon. Mine placement
//   is determined per-reveal: P(mine) = mines_remaining / tiles_remaining.
//   No pre-derived mine layout is stored — the player cannot read on-chain
//   state to predict which tiles are safe.
//
// Game flow (v3):
//   1. place_bet with mines_count (1-13).
//      RESERVE(max_payout). Game is ACTIVE. No wakeup — player acts first.
//
//   2. bet_action — player picks a tile (action=1, tile 0-24) or cashes out (action=2).
//      REVEAL: store pending tile, schedule_wakeup(betID, 0), phase → WAITING_RNG.
//      CASHOUT: settle immediately at current multiplier.
//
//   3. block_update — resolve pending reveals.
//      For each wakeup bet: get_rng(height-1), determine mine/safe.
//        - MINE: settle(0, loss).
//        - SAFE: update state. If max_reveals → auto-cashout.
//
//   4. info — returns game metadata JSON.
//
// Payout math (precomputed table):
//   fairMultBP[mines][reveals] = 10000 * prod((25-i)/(25-mines-i)) for i=0..k-1
//   payout = stake * fairMultBP * (10000 - house_edge_bp) / (10000 * 10000)
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

//go:wasmimport env emit_event
func host_emit_event(topicPtr, topicLen, dataPtr, dataLen uint32)

// ---------------------------------------------------------------------------
// Memory management
// ---------------------------------------------------------------------------

//export alloc
func alloc(size uint32) *byte {
	if size == 0 {
		size = 1
	}
	buf := make([]byte, size)
	return &buf[0]
}

//export dealloc
func dealloc(ptr *byte, size uint32) {}

// ---------------------------------------------------------------------------
// Game constants
// ---------------------------------------------------------------------------

const (
	boardSize     = 25
	maxMines      = 13
	minMines      = 1
	houseEdgeBP   = 200
	maxReveals    = 24
	timeoutBlocks = 100

	kindWin  = 1
	kindLoss = 2
)

// Entry layout (24 bytes):
//
//	[0..7]   betID          u64
//	[8..15]  stake          u64
//	[16]     mines_count    u8
//	[17]     revealed       u8
//	[18]     phase          u8
//	[19..22] board_mask     u32 (bitmask of opened tiles)
//	[23]     pending_tile   u8 (tile awaiting RNG resolution)
const entrySize = 24

const (
	phaseActive     byte = 0
	phaseWaitingRNG byte = 1
)

// ---------------------------------------------------------------------------
// Precomputed fair multiplier table (basis points)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// init_game — precompute fair multiplier table, store in KV
// ---------------------------------------------------------------------------

//export init_game
func init_game(sentinelID, bankrollID, calculatorID uint64) {
	// Precompute fairMultBP[mines-1][reveals-1] and store as one KV blob.
	// 13 mines × 24 reveals × 8 bytes = 2496 bytes.
	tableSize := maxMines * (boardSize - 1) * 8
	table := make([]byte, tableSize)

	for m := uint64(1); m <= maxMines; m++ {
		safe := uint64(boardSize) - m
		num := uint64(1)
		den := uint64(1)
		for k := uint64(1); k <= safe && k < boardSize; k++ {
			num *= (uint64(boardSize) - k + 1)
			den *= (safe - k + 1)
			g := gcd(num, den)
			num /= g
			den /= g
			val := num * 10000 / den
			off := ((m - 1) * (boardSize - 1) * 8) + ((k - 1) * 8)
			binary.LittleEndian.PutUint64(table[off:], val)
		}
	}
	kvSet([]byte("mult_table"), table)
	emitJSON("init", "table_size", uint64(tableSize))
}

// getFairMultBP reads precomputed multiplier from KV table.
func getFairMultBP(minesIdx, revealsIdx uint64) uint64 {
	table := kvGetBytes([]byte("mult_table"))
	if table == nil {
		return 0
	}
	off := (minesIdx * (boardSize - 1) * 8) + (revealsIdx * 8)
	if int(off+8) > len(table) {
		return 0
	}
	return binary.LittleEndian.Uint64(table[off:])
}

func betKey(betID uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'e'
	binary.LittleEndian.PutUint64(buf[1:], betID)
	return buf
}

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// ---------------------------------------------------------------------------
// place_bet — called during MsgPlaceBet tx
// ---------------------------------------------------------------------------

//export place_bet
func place_bet(betID, bankrollID, calculatorID, stake uint64, paramsPtr, paramsLen uint32) uint32 {
	// Params layout: sender(20) + mines_count(1) = 21 bytes.
	params := ptrToSlice(paramsPtr, paramsLen)
	if len(params) < 21 {
		return 11 // invalid params
	}
	// sender := params[0:20] — not used by mines
	minesCount := params[20]
	if minesCount < minMines || minesCount > maxMines {
		return 12 // mines count out of range
	}

	// Compute max payout.
	safe := uint64(boardSize) - uint64(minesCount)
	effectiveMaxReveals := uint64(maxReveals)
	if effectiveMaxReveals > safe {
		effectiveMaxReveals = safe
	}

	maxFairBP := getFairMultBP(uint64(minesCount-1), effectiveMaxReveals-1)
	maxEdgedBP := maxFairBP * (10000 - houseEdgeBP) / 10000
	maxPayout := mulDiv(stake, maxEdgedBP, 10000)

	// Reserve bankroll liquidity.
	if host_reserve(betID, maxPayout) != 0 {
		return 3 // insufficient liquidity
	}

	// Store entry state in KV.
	entry := make([]byte, entrySize)
	binary.LittleEndian.PutUint64(entry[0:], betID)
	binary.LittleEndian.PutUint64(entry[8:], stake)
	entry[16] = minesCount
	entry[17] = 0           // revealed
	entry[18] = phaseActive // active immediately — no initial RNG needed
	// board_mask [19:23] = 0
	// pending_tile [23] = 0
	kvSet(betKey(betID), entry)

	// Emit bet event. No wakeup needed — player acts first via bet_action.
	emitJSON("bet", "entry_id", betID, "stake", stake, "mines", uint64(minesCount), "max_payout", maxPayout)

	return 0 // success
}

// ---------------------------------------------------------------------------
// bet_action — called during MsgBetAction tx
// ---------------------------------------------------------------------------

//export bet_action
func bet_action(betID uint64, actionPtr, actionLen uint32) uint32 {
	action := ptrToSlice(actionPtr, actionLen)
	if len(action) < 1 {
		return 1 // invalid action
	}

	switch action[0] {
	case 1: // reveal
		return handleReveal(betID, action[1:])
	case 2: // cashout
		return handleCashout(betID)
	default:
		return 2 // unknown action
	}
}

// ---------------------------------------------------------------------------
// REVEAL — store tile choice, schedule wakeup for RNG
// ---------------------------------------------------------------------------

func handleReveal(betID uint64, payload []byte) uint32 {
	entry := kvGetBytes(betKey(betID))
	if entry == nil || len(entry) < entrySize {
		return 30 // no active bet
	}
	if entry[18] != phaseActive {
		return 31 // not in active phase
	}
	if len(payload) < 1 {
		return 33 // missing tile
	}
	tile := payload[0]
	if tile >= boardSize {
		return 34 // tile out of range
	}
	boardMask := binary.LittleEndian.Uint32(entry[19:23])
	if boardMask&(1<<tile) != 0 {
		return 35 // tile already opened
	}

	// Store pending tile, transition to waiting for RNG.
	entry[18] = phaseWaitingRNG
	entry[23] = tile
	kvSet(betKey(betID), entry)

	// Schedule wakeup at next block (0 = keeper interprets as currentHeight+1).
	schedule_wakeup(betID, 0)

	// Emit reveal pending event.
	emitJSON("reveal_pending", "entry_id", betID, "tile", uint64(tile))

	return 0 // success
}

// ---------------------------------------------------------------------------
// CASHOUT — settle at current multiplier
// ---------------------------------------------------------------------------

func handleCashout(betID uint64) uint32 {
	entry := kvGetBytes(betKey(betID))
	if entry == nil || len(entry) < entrySize {
		return 40 // no active session
	}
	if entry[18] != phaseActive {
		return 41 // not in active phase
	}
	revealed := entry[17]
	if revealed == 0 {
		return 43 // must reveal at least 1 tile
	}

	minesCount := entry[16]
	stake := binary.LittleEndian.Uint64(entry[8:16])

	currentMultBP := getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
	edgedMultBP := currentMultBP * (10000 - houseEdgeBP) / 10000
	payout := mulDiv(stake, edgedMultBP, 10000)

	// Settle as win.
	host_settle(betID, payout, kindWin)
	clearBet(betID)

	// Emit cashout event.
	emitJSON("cashout", "entry_id", betID, "revealed", uint64(revealed), "mult_bp", edgedMultBP, "payout", payout)

	return 0 // success
}

// ---------------------------------------------------------------------------
// block_update — resolve pending reveals via RNG
// ---------------------------------------------------------------------------

//export block_update
func block_update(height uint64) {
	count := host_get_bet_count()
	for i := uint32(0); i < count; i++ {
		betID := host_get_bet_id(i)
		resolveReveal(betID, height)
	}
}

func resolveReveal(betID uint64, height uint64) {
	entry := kvGetBytes(betKey(betID))
	if entry == nil || len(entry) < entrySize {
		return
	}
	if entry[18] != phaseWaitingRNG {
		return
	}
	// Get RNG seed from previous block.
	rngBuf := make([]byte, 32)
	ok := host_get_rng(height-1, uint32(uintptr(unsafe.Pointer(&rngBuf[0]))))
	if ok == 0 {
		// RNG not available — reschedule for next block.
		schedule_wakeup(betID, height+1)
		return
	}

	stake := binary.LittleEndian.Uint64(entry[8:16])
	minesCount := entry[16]
	revealed := entry[17]
	boardMask := binary.LittleEndian.Uint32(entry[19:23])
	tile := entry[23]

	safe := uint64(boardSize) - uint64(minesCount)
	effectiveMaxReveals := uint64(maxReveals)
	if effectiveMaxReveals > safe {
		effectiveMaxReveals = safe
	}

	// Determine mine/safe using RNG.
	// P(mine) = mines_count / (board_size - revealed)
	remaining := uint64(boardSize) - uint64(revealed)
	// Mix betID into entropy so each bot gets independent randomness.
	var betBuf [8]byte
	binary.BigEndian.PutUint64(betBuf[:], betID)
	entropy := make([]byte, len(rngBuf)+8)
	copy(entropy, rngBuf)
	copy(entropy[len(rngBuf):], betBuf[:])
	h := sha256sum(entropy)
	rngVal := binary.BigEndian.Uint64(h[0:8]) % remaining
	isMine := rngVal < uint64(minesCount)

	if isMine {
		// Mine hit — loss.
		host_settle(betID, 0, kindLoss)
		clearBet(betID)
		emitJSON("mine_hit", "entry_id", betID, "tile", uint64(tile), "mines", uint64(minesCount), "revealed", uint64(revealed))
		return
	}

	// Safe tile.
	revealed++
	boardMask |= 1 << tile
	entry[17] = revealed
	binary.LittleEndian.PutUint32(entry[19:23], boardMask)
	entry[18] = phaseActive
	kvSet(betKey(betID), entry)

	currentMultBP := getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
	edgedMultBP := currentMultBP * (10000 - houseEdgeBP) / 10000
	payout := mulDiv(stake, edgedMultBP, 10000)

	// Max reveals reached — auto-cashout.
	if uint64(revealed) >= effectiveMaxReveals {
		host_settle(betID, payout, kindWin)
		clearBet(betID)
		emitJSON("tile_safe", "entry_id", betID, "tile", uint64(tile), "revealed", uint64(revealed), "mult_bp", edgedMultBP, "payout", payout, "auto_cashout", uint64(1))
		return
	}

	// Game continues.
	emitJSON("tile_safe", "entry_id", betID, "tile", uint64(tile), "revealed", uint64(revealed), "mult_bp", edgedMultBP, "next_payout", payout)
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

//export info
func info() *byte {
	data := []byte(`{"name":"Mines","engine":"mines","mode":"v3","house_edge_bp":200,"developer":"ExoHash","description":"5x5 minefield — reveal tiles, avoid mines, cash out"}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
}

// ---------------------------------------------------------------------------
// Session helpers
// ---------------------------------------------------------------------------

func clearBet(betID uint64) {
	kvSet(betKey(betID), make([]byte, entrySize))
}

// ---------------------------------------------------------------------------
// KV helpers
// ---------------------------------------------------------------------------

func kvSet(key, value []byte) {
	if len(value) == 0 {
		value = []byte{0}
	}
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
// Pointer helpers
// ---------------------------------------------------------------------------

func ptrToSlice(ptr, length uint32) []byte {
	if length == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
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
// Math — 128-bit multiply/divide for large payout products
// ---------------------------------------------------------------------------

func mulDiv(a, b, c uint64) uint64 {
	hi, lo := mul64(a, b)
	return div128(hi, lo, c)
}

func mul64(a, b uint64) (uint64, uint64) {
	aHi, aLo := a>>32, a&0xFFFFFFFF
	bHi, bLo := b>>32, b&0xFFFFFFFF
	mid1, mid2 := aHi*bLo, aLo*bHi
	lo := aLo * bLo
	hi := aHi * bHi
	carry := ((lo >> 32) + (mid1 & 0xFFFFFFFF) + (mid2 & 0xFFFFFFFF)) >> 32
	lo += (mid1 << 32) + (mid2 << 32)
	hi += (mid1 >> 32) + (mid2 >> 32) + carry
	return hi, lo
}

func div128(hi, lo, d uint64) uint64 {
	if hi == 0 {
		return lo / d
	}
	if hi < d {
		top := (hi << 32) | (lo >> 32)
		q1 := top / d
		rem := top % d
		bot := (rem << 32) | (lo & 0xFFFFFFFF)
		return (q1 << 32) | (bot / d)
	}
	return lo / d
}

// ---------------------------------------------------------------------------
// SHA-256 (inline — no crypto/sha256, FIPS panics in WASM)
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
