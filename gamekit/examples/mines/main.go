// Mines calculator v3 — host-callback protocol.
//
// 5x5 minefield. Player reveals tiles, avoids mines, cashes out.
//
// Rules:
//   - Max 5 reveals, then auto-cashout
//   - 40 blocks (~20s) inactivity timeout:
//     - If revealed > 0 → auto-cashout at current multiplier
//     - If revealed == 0 → refund
//   - Mine placement per-reveal: P(mine) = mines_remaining / tiles_remaining
//
// Events:
//   state   — every block_update: {phase, bet_id, mines, revealed, mult_bp, payout, timeout_left}
//   joined  — on place_bet: {bet_id, addr, stake, mines}
//   reveal  — on tile reveal: {bet_id, addr, tile, safe, revealed, mult_bp, payout}
//   settled — on game end: {bet_id, addr, payout, kind, reason}
//
// Exports: alloc, dealloc, place_bet, bet_action, block_update, query, info, init_game
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

//go:wasmimport env cancel_wakeup
func cancel_wakeup(betID uint64)

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

//go:wasmimport env get_bettor
func host_get_bettor(betID uint64, outPtr uint32) uint32

//go:wasmimport env emit_event
func host_emit_event(topicPtr, topicLen, dataPtr, dataLen uint32)

// ---------------------------------------------------------------------------
// Memory
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
// Constants
// ---------------------------------------------------------------------------

const (
	boardSize   = 25
	maxMines    = 13
	minMines    = 1
	houseEdgeBP = 200
	maxReveals  = 5  // auto-cashout after 5 reveals
	timeoutBlks = 40 // ~20s at 500ms blocks

	kindWin    uint32 = 1
	kindLoss   uint32 = 2
	kindRefund uint32 = 3

	phaseActive     byte = 0
	phaseWaitingRNG byte = 1

	maxAddrBuf = 64
)

// Bet layout (32 bytes):
//   [0..7]   bet_id        u64
//   [8..15]  stake         u64
//   [16]     mines_count   u8
//   [17]     revealed      u8
//   [18]     phase         u8
//   [19..22] board_mask    u32 (bitmask of opened tiles)
//   [23]     pending_tile  u8
//   [24..31] timeout_height u64
const betSize = 32

// ---------------------------------------------------------------------------
// KV keys
// ---------------------------------------------------------------------------

func betKey(id uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'b'
	binary.LittleEndian.PutUint64(buf[1:], id)
	return buf
}

// ---------------------------------------------------------------------------
// init_game — precompute multiplier table
// ---------------------------------------------------------------------------

//export init_game
func init_game(sentinelID, bankrollID, calculatorID uint64) {
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
}

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

func gcd(a, b uint64) uint64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// ---------------------------------------------------------------------------
// place_bet
// ---------------------------------------------------------------------------

//export place_bet
func place_bet(betID, bankrollID, calculatorID, stake uint64, paramsPtr, paramsLen uint32) uint32 {
	params := ptrToSlice(paramsPtr, paramsLen)
	if len(params) < 21 {
		return 11
	}
	minesCount := params[20]
	if minesCount < minMines || minesCount > maxMines {
		return 12
	}

	// Max payout based on maxReveals (not full board).
	safe := uint64(boardSize) - uint64(minesCount)
	reveals := uint64(maxReveals)
	if reveals > safe {
		reveals = safe
	}
	maxFairBP := getFairMultBP(uint64(minesCount-1), reveals-1)
	maxEdgedBP := maxFairBP * (10000 - houseEdgeBP) / 10000
	maxPayout := mulDiv(stake, maxEdgedBP, 10000)

	if host_reserve(betID, maxPayout) != 0 {
		return 3
	}

	// Store bet.
	bet := make([]byte, betSize)
	binary.LittleEndian.PutUint64(bet[0:], betID)
	binary.LittleEndian.PutUint64(bet[8:], stake)
	bet[16] = minesCount
	bet[17] = 0           // revealed
	bet[18] = phaseActive
	// board_mask [19:23] = 0
	// pending_tile [23] = 0
	// timeout_height [24:32] = 0 (set on first wakeup)
	kvSet(betKey(betID), bet)

	// Schedule timeout wakeup.
	schedule_wakeup(betID, 0) // next block starts timeout countdown

	addr := getBettorAddr(betID)
	emitJSON("joined", "bet_id", betID, "addr", addr, "stake", stake, "mines", uint64(minesCount))
	return 0
}

// ---------------------------------------------------------------------------
// bet_action
// ---------------------------------------------------------------------------

//export bet_action
func bet_action(betID uint64, actionPtr, actionLen uint32) uint32 {
	action := ptrToSlice(actionPtr, actionLen)
	if len(action) < 1 {
		return 1
	}
	switch action[0] {
	case 1:
		return handleReveal(betID, action[1:])
	case 2:
		return handleCashout(betID)
	default:
		return 2
	}
}

func handleReveal(betID uint64, payload []byte) uint32 {
	bet := kvGetBytes(betKey(betID))
	if bet == nil || len(bet) < betSize {
		return 30
	}
	if bet[18] != phaseActive {
		return 31
	}
	if len(payload) < 1 {
		return 33
	}
	tile := payload[0]
	if tile >= boardSize {
		return 34
	}
	boardMask := binary.LittleEndian.Uint32(bet[19:23])
	if boardMask&(1<<tile) != 0 {
		return 35
	}

	bet[18] = phaseWaitingRNG
	bet[23] = tile
	kvSet(betKey(betID), bet)

	// Reschedule wakeup for RNG resolution (cancels timeout, replaced by RNG wakeup).
	schedule_wakeup(betID, 0)
	return 0
}

func handleCashout(betID uint64) uint32 {
	bet := kvGetBytes(betKey(betID))
	if bet == nil || len(bet) < betSize {
		return 40
	}
	if bet[18] != phaseActive {
		return 41
	}
	revealed := bet[17]
	if revealed == 0 {
		return 43
	}

	minesCount := bet[16]
	stake := binary.LittleEndian.Uint64(bet[8:16])
	currentMultBP := getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
	edgedMultBP := currentMultBP * (10000 - houseEdgeBP) / 10000
	payout := mulDiv(stake, edgedMultBP, 10000)

	host_settle(betID, payout, kindWin)
	cancel_wakeup(betID)
	addr := getBettorAddr(betID)
	emitJSON("settled", "bet_id", betID, "addr", addr, "payout", payout, "kind", uint64(kindWin), "reason", "cashout")
	clearBet(betID)
	return 0
}

// ---------------------------------------------------------------------------
// block_update — resolve reveals + timeout
// ---------------------------------------------------------------------------

//export block_update
func block_update(height uint64) {
	count := host_get_bet_count()
	for i := uint32(0); i < count; i++ {
		betID := host_get_bet_id(i)
		processBet(betID, height)
	}
}

func processBet(betID uint64, height uint64) {
	bet := kvGetBytes(betKey(betID))
	if bet == nil || len(bet) < betSize {
		return
	}

	phase := bet[18]

	// Handle pending reveal.
	if phase == phaseWaitingRNG {
		resolveReveal(betID, bet, height)
		return
	}

	// Handle timeout for active bets.
	if phase == phaseActive {
		timeoutH := binary.LittleEndian.Uint64(bet[24:32])
		if timeoutH == 0 {
			// First wakeup — set timeout deadline.
			timeoutH = height + timeoutBlks
			binary.LittleEndian.PutUint64(bet[24:], timeoutH)
			kvSet(betKey(betID), bet)
			schedule_wakeup(betID, height+1)
			emitBetState(betID, bet, height)
			return
		}

		if height >= timeoutH {
			// Timeout reached.
			handleTimeout(betID, bet)
			return
		}

		// Still waiting — reschedule and emit state.
		schedule_wakeup(betID, height+1)
		emitBetState(betID, bet, height)
		return
	}
}

func resolveReveal(betID uint64, bet []byte, height uint64) {
	rngBuf := make([]byte, 32)
	ok := host_get_rng(height-1, uint32(uintptr(unsafe.Pointer(&rngBuf[0]))))
	if ok == 0 {
		schedule_wakeup(betID, height+1)
		return
	}

	stake := binary.LittleEndian.Uint64(bet[8:16])
	minesCount := bet[16]
	revealed := bet[17]
	boardMask := binary.LittleEndian.Uint32(bet[19:23])
	tile := bet[23]

	safe := uint64(boardSize) - uint64(minesCount)
	effectiveMax := uint64(maxReveals)
	if effectiveMax > safe {
		effectiveMax = safe
	}

	// Determine mine/safe.
	remaining := uint64(boardSize) - uint64(revealed)
	var betBuf [8]byte
	binary.BigEndian.PutUint64(betBuf[:], betID)
	entropy := make([]byte, len(rngBuf)+8)
	copy(entropy, rngBuf)
	copy(entropy[len(rngBuf):], betBuf[:])
	h := sha256sum(entropy)
	rngVal := binary.BigEndian.Uint64(h[0:8]) % remaining
	isMine := rngVal < uint64(minesCount)

	addr := getBettorAddr(betID)

	if isMine {
		host_settle(betID, 0, kindLoss)
		emitJSON("reveal", "bet_id", betID, "addr", addr, "tile", uint64(tile), "safe", uint64(0), "revealed", uint64(revealed), "mult_bp", uint64(0), "payout", uint64(0))
		emitJSON("settled", "bet_id", betID, "addr", addr, "payout", uint64(0), "kind", uint64(kindLoss), "reason", "mine")
		clearBet(betID)
		return
	}

	// Safe tile.
	revealed++
	boardMask |= 1 << tile
	bet[17] = revealed
	binary.LittleEndian.PutUint32(bet[19:23], boardMask)
	bet[18] = phaseActive

	currentMultBP := getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
	edgedMultBP := currentMultBP * (10000 - houseEdgeBP) / 10000
	payout := mulDiv(stake, edgedMultBP, 10000)

	// Reset timeout.
	timeoutH := height + timeoutBlks
	binary.LittleEndian.PutUint64(bet[24:], timeoutH)
	kvSet(betKey(betID), bet)

	emitJSON("reveal", "bet_id", betID, "addr", addr, "tile", uint64(tile), "safe", uint64(1), "revealed", uint64(revealed), "mult_bp", edgedMultBP, "payout", payout)

	// Auto-cashout at max reveals.
	if uint64(revealed) >= effectiveMax {
		host_settle(betID, payout, kindWin)
		emitJSON("settled", "bet_id", betID, "addr", addr, "payout", payout, "kind", uint64(kindWin), "reason", "max_reveals")
		clearBet(betID)
		return
	}

	// Continue — schedule timeout wakeup.
	schedule_wakeup(betID, height+1)
}

func handleTimeout(betID uint64, bet []byte) {
	revealed := bet[17]
	addr := getBettorAddr(betID)

	if revealed == 0 {
		// No tiles opened — refund.
		stake := binary.LittleEndian.Uint64(bet[8:16])
		host_settle(betID, stake, kindRefund)
		emitJSON("settled", "bet_id", betID, "addr", addr, "payout", stake, "kind", uint64(kindRefund), "reason", "timeout_refund")
		clearBet(betID)
		return
	}

	// Tiles opened — auto-cashout at current multiplier.
	minesCount := bet[16]
	stake := binary.LittleEndian.Uint64(bet[8:16])
	currentMultBP := getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
	edgedMultBP := currentMultBP * (10000 - houseEdgeBP) / 10000
	payout := mulDiv(stake, edgedMultBP, 10000)

	host_settle(betID, payout, kindWin)
	emitJSON("settled", "bet_id", betID, "addr", addr, "payout", payout, "kind", uint64(kindWin), "reason", "timeout_cashout")
	clearBet(betID)
}

// emitBetState emits a state event for an active bet.
func emitBetState(betID uint64, bet []byte, height uint64) {
	revealed := bet[17]
	minesCount := bet[16]
	timeoutH := binary.LittleEndian.Uint64(bet[24:32])
	timeoutLeft := uint64(0)
	if timeoutH > height {
		timeoutLeft = timeoutH - height
	}

	multBP := uint64(10000)
	payout := uint64(0)
	if revealed > 0 {
		multBP = getFairMultBP(uint64(minesCount-1), uint64(revealed-1))
		multBP = multBP * (10000 - houseEdgeBP) / 10000
		stake := binary.LittleEndian.Uint64(bet[8:16])
		payout = mulDiv(stake, multBP, 10000)
	}

	emitJSON("state",
		"phase", "active",
		"bet_id", betID,
		"mines", uint64(minesCount),
		"revealed", uint64(revealed),
		"mult_bp", multBP,
		"payout", payout,
		"timeout_left", timeoutLeft,
	)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clearBet(betID uint64) {
	kvDelete(betKey(betID))
}

func getBettorAddr(betID uint64) string {
	buf := make([]byte, maxAddrBuf)
	n := host_get_bettor(betID, uint32(uintptr(unsafe.Pointer(&buf[0]))))
	if n == 0 || n > maxAddrBuf {
		return ""
	}
	return string(buf[:n])
}

func ptrToSlice(ptr, length uint32) []byte {
	if length == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

// ---------------------------------------------------------------------------
// query — returns active bet state (if any)
// ---------------------------------------------------------------------------

//export query
func query() *byte {
	// No persistent game state to query for mines — each bet is independent.
	// Return empty JSON object.
	data := []byte(`{}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

//export info
func info() *byte {
	data := []byte(`{
		"name":"Mines",
		"engine":"mines",
		"mode":"v3",
		"house_edge_bp":200,
		"max_reveals":5,
		"timeout_blocks":40,
		"developer":"ExoHash",
		"description":"5x5 minefield — reveal tiles, avoid mines, cash out. Max 5 reveals, 20s timeout.",
		"errors":{
			"place_bet":{
				"3":"Insufficient bankroll liquidity",
				"11":"Invalid parameters — expected sender(20) + mines_count(1)",
				"12":"Mines count out of range — must be between 1 and 13"
			},
			"bet_action":{
				"1":"Invalid action format",
				"2":"Unknown action type — use 1 (reveal) or 2 (cashout)",
				"30":"No active bet found",
				"31":"Bet not in active phase — waiting for RNG",
				"33":"Missing tile index in reveal action",
				"34":"Tile index out of range — must be 0 to 24",
				"35":"Tile already revealed",
				"40":"No active session found",
				"41":"Session not in active phase",
				"43":"Must reveal at least 1 tile before cashing out"
			}
		}
	}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
}

// ---------------------------------------------------------------------------
// KV helpers
// ---------------------------------------------------------------------------

func kvGetBytes(key []byte) []byte {
	if len(key) == 0 {
		return nil
	}
	packed := kv_get(uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)))
	if packed == 0 {
		return nil
	}
	ptr := uint32(packed >> 32)
	length := uint32(packed & 0xFFFFFFFF)
	if length == 0 {
		return nil
	}
	val := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
	if length == 1 && val[0] == 0xFF {
		return nil
	}
	return val
}

func kvSet(key, value []byte) {
	if len(key) == 0 || len(value) == 0 {
		return
	}
	kv_set(
		uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)),
		uint32(uintptr(unsafe.Pointer(&value[0]))), uint32(len(value)),
	)
}

var kvDeleteSentinel = []byte{0xFF}

func kvDelete(key []byte) {
	kv_set(
		uint32(uintptr(unsafe.Pointer(&key[0]))), uint32(len(key)),
		uint32(uintptr(unsafe.Pointer(&kvDeleteSentinel[0]))), 1,
	)
}

// ---------------------------------------------------------------------------
// Math
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
	buf := make([]byte, 0, 256)
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
// SHA-256 (inline)
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
			h = g; g = f; f = e; e = d + temp1; d = c; c = b; b = a; a = temp1 + temp2
		}
		h0 += a; h1 += b; h2 += c; h3 += d; h4 += e; h5 += f; h6 += g; h7 += h
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

func rotr32(x uint32, n uint) uint32 { return (x >> n) | (x << (32 - n)) }

func main() {}
