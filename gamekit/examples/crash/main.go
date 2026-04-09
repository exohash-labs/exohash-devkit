// Crash calculator v3 — host-callback protocol.
//
// Multiplayer crash game with rising multiplier and random crash point.
//
// Phases: open → tick → crashed → open → ...
//
// Events (game UI uses only these, chain events for accounting):
//   state   — every block: {phase, round, mult_bp, tick, blocks_left, players, active, cashed, stake}
//   joined  — on place_bet: {bet_id, addr, stake, players}
//   cashout — on voluntary exit: {bet_id, addr, mult_bp, payout}
//   settled — on crash/loss per player: {bet_id, addr, payout, kind}
//
// query() — called on page load: {round, phase, mult_bp, tick, blocks_left, players:[...], history:[...]}
//
// Exports: alloc, dealloc, place_bet, bet_action, block_update, query, info, init_game
package main

import (
	"encoding/binary"
	"math"
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
// Constants & layouts
// ---------------------------------------------------------------------------

const (
	kindWin  uint32 = 1
	kindLoss uint32 = 2

	phaseOpen    byte = 0
	phaseTick    byte = 1
	phaseCrashed byte = 2

	statusActive     byte = 0
	statusCashoutReq byte = 1
	statusSettled    byte = 2

	cfgSize   = 48 // 40 + crashedBlocks(8)
	roundSize = 33
	betSize   = 18 // bet_id(8)+stake(8)+status(1)+pad(1)

	maxHistory     = 20
	crashedBlocks  = 5 // how many blocks to linger in crashed phase
	maxAddrBufSize = 64
)

// Config layout (48 bytes):
//   [0..7]   house_edge_bp
//   [8..15]  tick_growth_bp
//   [16..23] max_multiplier_bp
//   [24..31] max_ticks (0=unlimited)
//   [32..39] join_window_blocks
//   [40..47] crashed_cooldown_blocks
//
// Round layout (33 bytes):
//   [0..7]   current_mult (bp)
//   [8..15]  ticks_elapsed
//   [16..23] pending_height
//   [24]     phase
//   [25..32] bet_count

var (
	keyCfg     = []byte("cfg")
	keyRound   = []byte("r")
	keyBetList = []byte("bl")
	keyTrigger = []byte("tg")
	keyHistory = []byte("ch")
)

func betKey(id uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'b'
	binary.LittleEndian.PutUint64(buf[1:], id)
	return buf
}

func cashoutKey(height uint64) []byte {
	buf := make([]byte, 9)
	buf[0] = 'c'
	binary.LittleEndian.PutUint64(buf[1:], height)
	return buf
}

// ---------------------------------------------------------------------------
// init_game
// ---------------------------------------------------------------------------

//export init_game
func init_game(sentinelID, bankrollID, calculatorID uint64) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, sentinelID)
	kvSet(keyTrigger, buf)

	round := newRound()
	kvSet(keyRound, round)

	rnBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(rnBuf, 1)
	kvSet([]byte("rn"), rnBuf)

	schedule_wakeup(sentinelID, 0)

	cfg := kvGetOrInitCfg()
	joinWindow := binary.LittleEndian.Uint64(cfg[32:40])
	emitState("open", getRoundNumber(), uint64(10000), 0, joinWindow, 0, 0, 0, 0)
}

// ---------------------------------------------------------------------------
// place_bet
// ---------------------------------------------------------------------------

//export place_bet
func place_bet(betID, bankrollID, calculatorID, stake uint64, paramsPtr, paramsLen uint32) uint32 {
	cfg := kvGetOrInitCfg()

	round := kvGetRound()
	if round == nil {
		round = newRound()
		kvDelete(keyBetList)
	}
	if round[24] != phaseOpen {
		return 10
	}
	if kvGetBytes(betKey(betID)) != nil {
		return 11
	}

	// Store bet.
	bet := make([]byte, betSize)
	binary.LittleEndian.PutUint64(bet[0:], betID)
	binary.LittleEndian.PutUint64(bet[8:], stake)
	bet[16] = statusActive
	kvSet(betKey(betID), bet)
	appendBetID(betID)

	// Update count.
	count := binary.LittleEndian.Uint64(round[25:33])
	count++
	binary.LittleEndian.PutUint64(round[25:], count)
	kvSet(keyRound, round)

	// Reserve max payout.
	maxMult := binary.LittleEndian.Uint64(cfg[16:24])
	maxPayout := safeMulDiv(stake, maxMult, 10000)
	if host_reserve(betID, maxPayout) != 0 {
		return 3
	}

	addr := getBettorAddr(betID)
	emitJSON("joined", "bet_id", betID, "addr", addr, "stake", stake, "players", count)
	return 0
}

// ---------------------------------------------------------------------------
// bet_action (cashout request)
// ---------------------------------------------------------------------------

//export bet_action
func bet_action(betID uint64, actionPtr, actionLen uint32) uint32 {
	round := kvGetRound()
	if round == nil || round[24] != phaseTick {
		return 20
	}
	bet := kvGetBytes(betKey(betID))
	if bet == nil || bet[16] != statusActive {
		return 21
	}
	bet[16] = statusCashoutReq
	kvSet(betKey(betID), bet)
	pendingH := binary.LittleEndian.Uint64(round[16:24])
	appendCashout(pendingH, betID)
	return 0
}

// ---------------------------------------------------------------------------
// block_update
// ---------------------------------------------------------------------------

//export block_update
func block_update(height uint64) {
	count := host_get_bet_count()
	if count == 0 {
		return
	}
	round := kvGetRound()
	if round == nil {
		return
	}
	switch round[24] {
	case phaseOpen:
		handleOpen(height, round)
	case phaseTick:
		handleTick(height, round)
	case phaseCrashed:
		handleCrashed(height, round)
	}
}

// ---------------------------------------------------------------------------
// Phase: OPEN — join window countdown
// ---------------------------------------------------------------------------

func handleOpen(height uint64, round []byte) {
	cfg := kvGetOrInitCfg()
	joinWindow := binary.LittleEndian.Uint64(cfg[32:40])

	waitKey := []byte("wt")
	waitBuf := kvGetBytes(waitKey)
	var remaining uint64
	if waitBuf == nil {
		remaining = joinWindow
	} else {
		remaining = binary.LittleEndian.Uint64(waitBuf)
	}

	count := binary.LittleEndian.Uint64(round[25:33])
	_, _, totalStake := countActive()
	rn := getRoundNumber()

	if remaining > 1 {
		remaining--
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint64(buf, remaining)
		kvSet(waitKey, buf)
		reschedule(height + 1)
		emitState("open", rn, 10000, 0, remaining, count, count, 0, totalStake)
		return
	}

	// Transition to tick phase.
	kvDelete(waitKey)
	round[24] = phaseTick
	binary.LittleEndian.PutUint64(round[16:], height)
	kvSet(keyRound, round)
	reschedule(height + 1)
	emitState("tick", rn, 10000, 0, 0, count, count, 0, totalStake)
}

// ---------------------------------------------------------------------------
// Phase: TICK — multiplier climbing
// ---------------------------------------------------------------------------

func handleTick(height uint64, round []byte) {
	cfg := kvGetOrInitCfg()

	pendingHeight := binary.LittleEndian.Uint64(round[16:24])
	currentMult := binary.LittleEndian.Uint64(round[0:8])
	ticksElapsed := binary.LittleEndian.Uint64(round[8:16])
	houseEdge := binary.LittleEndian.Uint64(cfg[0:8])
	tickGrowth := binary.LittleEndian.Uint64(cfg[8:16])
	maxMult := binary.LittleEndian.Uint64(cfg[16:24])
	maxTicks := binary.LittleEndian.Uint64(cfg[24:32])

	// Get RNG.
	rngBuf := make([]byte, 32)
	ok := host_get_rng(pendingHeight, uint32(uintptr(unsafe.Pointer(&rngBuf[0]))))
	if ok == 0 {
		reschedule(height + 1)
		return
	}

	// Next multiplier.
	nextMult := currentMult * (10000 + tickGrowth) / 10000
	if nextMult > maxMult {
		nextMult = maxMult
	}
	if nextMult <= currentMult {
		nextMult = currentMult + 1
	}

	// Crash probability.
	var probSurvive float64
	if ticksElapsed == 0 {
		edge := float64(houseEdge) / 10000.0
		probSurvive = (1.0 - edge) * (float64(currentMult) / float64(nextMult))
	} else {
		probSurvive = float64(currentMult) / float64(nextMult)
	}
	if probSurvive > 1.0 {
		probSurvive = 1.0
	}

	rngVal := getUniformProb(rngBuf, pendingHeight)
	crashed := rngVal >= probSurvive
	rn := getRoundNumber()

	// Load cashouts.
	cashoutIDs := loadAndDeleteCashoutIDs(pendingHeight)

	if crashed {
		pushHistory(currentMult)
		settleAllAsLoss(round)
		enterCrashed(round, height, rn, currentMult, ticksElapsed)
		return
	}

	// Settle cashouts as wins.
	for _, bid := range cashoutIDs {
		bet := kvGetBytes(betKey(bid))
		if bet == nil || bet[16] == statusSettled {
			continue
		}
		stake := binary.LittleEndian.Uint64(bet[8:16])
		payout := safeMulDiv(stake, nextMult, 10000)
		bet[16] = statusSettled
		kvSet(betKey(bid), bet)
		host_settle(bid, payout, kindWin)
		addr := getBettorAddr(bid)
		emitJSON("cashout", "bet_id", bid, "addr", addr, "mult_bp", nextMult, "payout", payout)
	}

	// Max ticks check.
	if maxTicks > 0 && ticksElapsed+1 >= maxTicks {
		pushHistory(nextMult)
		settleRemainingAsWin(round, nextMult)
		enterCrashed(round, height, rn, nextMult, ticksElapsed+1)
		return
	}

	// Max multiplier check.
	if nextMult >= maxMult {
		pushHistory(maxMult)
		settleRemainingAsWin(round, maxMult)
		enterCrashed(round, height, rn, maxMult, ticksElapsed)
		return
	}

	// Advance.
	binary.LittleEndian.PutUint64(round[0:], nextMult)
	binary.LittleEndian.PutUint64(round[8:], ticksElapsed+1)
	binary.LittleEndian.PutUint64(round[16:], height)
	kvSet(keyRound, round)

	active, cashed, totalStake := countActive()
	count := binary.LittleEndian.Uint64(round[25:33])
	emitState("tick", rn, nextMult, ticksElapsed+1, 0, count, active, cashed, totalStake)
	reschedule(height + 1)
}

// ---------------------------------------------------------------------------
// Phase: CRASHED — cooldown before next round
// ---------------------------------------------------------------------------

func enterCrashed(round []byte, height uint64, rn, crashMult, finalTick uint64) {
	round[24] = phaseCrashed
	// Store crash info: reuse pending_height field for cooldown counter.
	cfg := kvGetOrInitCfg()
	cooldown := binary.LittleEndian.Uint64(cfg[40:48])
	if cooldown == 0 {
		cooldown = crashedBlocks
	}
	binary.LittleEndian.PutUint64(round[16:], cooldown) // reuse as blocks_left
	// Keep mult and tick for display.
	binary.LittleEndian.PutUint64(round[0:], crashMult)
	binary.LittleEndian.PutUint64(round[8:], finalTick)
	kvSet(keyRound, round)

	count := binary.LittleEndian.Uint64(round[25:33])
	_, cashed, totalStake := countActive()
	emitState("crashed", rn, crashMult, finalTick, cooldown, count, 0, cashed, totalStake)
	reschedule(height + 1)
}

func handleCrashed(height uint64, round []byte) {
	remaining := binary.LittleEndian.Uint64(round[16:24])
	rn := getRoundNumber()
	crashMult := binary.LittleEndian.Uint64(round[0:8])
	finalTick := binary.LittleEndian.Uint64(round[8:16])
	count := binary.LittleEndian.Uint64(round[25:33])

	if remaining > 1 {
		remaining--
		binary.LittleEndian.PutUint64(round[16:], remaining)
		kvSet(keyRound, round)
		emitState("crashed", rn, crashMult, finalTick, remaining, count, 0, 0, 0)
		reschedule(height + 1)
		return
	}

	// Cooldown over — restart.
	markSettledAndRestart(height)
}

func markSettledAndRestart(height uint64) {
	betIDs := loadBetIDs()
	for _, bid := range betIDs {
		kvDelete(betKey(bid))
	}
	kvDelete(keyBetList)
	kvDelete([]byte("wt"))

	newR := newRound()
	kvSet(keyRound, newR)
	reschedule(height + 1)

	// Increment round number.
	rn := getRoundNumber() + 1
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, rn)
	kvSet([]byte("rn"), buf)

	cfg := kvGetOrInitCfg()
	joinWindow := binary.LittleEndian.Uint64(cfg[32:40])
	emitState("open", rn, 10000, 0, joinWindow, 0, 0, 0, 0)
}

// ---------------------------------------------------------------------------
// Settlement helpers
// ---------------------------------------------------------------------------

func settleAllAsLoss(round []byte) {
	betIDs := loadBetIDs()
	for _, bid := range betIDs {
		bet := kvGetBytes(betKey(bid))
		if bet == nil || bet[16] == statusSettled {
			continue
		}
		bet[16] = statusSettled
		kvSet(betKey(bid), bet)
		host_settle(bid, 0, kindLoss)
		addr := getBettorAddr(bid)
		emitJSON("settled", "bet_id", bid, "addr", addr, "payout", uint64(0), "kind", uint64(kindLoss))
	}
}

func settleRemainingAsWin(round []byte, multBP uint64) {
	betIDs := loadBetIDs()
	for _, bid := range betIDs {
		bet := kvGetBytes(betKey(bid))
		if bet == nil || bet[16] == statusSettled {
			continue
		}
		stake := binary.LittleEndian.Uint64(bet[8:16])
		payout := safeMulDiv(stake, multBP, 10000)
		bet[16] = statusSettled
		kvSet(betKey(bid), bet)
		host_settle(bid, payout, kindWin)
		addr := getBettorAddr(bid)
		emitJSON("settled", "bet_id", bid, "addr", addr, "payout", payout, "kind", uint64(kindWin))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newRound() []byte {
	round := make([]byte, roundSize)
	binary.LittleEndian.PutUint64(round[0:], 10000) // 1.00x
	round[24] = phaseOpen
	return round
}

func reschedule(height uint64) {
	trigBuf := kvGetBytes(keyTrigger)
	if trigBuf != nil {
		trigID := binary.LittleEndian.Uint64(trigBuf)
		schedule_wakeup(trigID, height)
	}
}

func getRoundNumber() uint64 {
	rnBuf := kvGetBytes([]byte("rn"))
	if rnBuf != nil && len(rnBuf) >= 8 {
		return binary.LittleEndian.Uint64(rnBuf)
	}
	return 1
}

func countActive() (active, cashed, totalStake uint64) {
	betIDs := loadBetIDs()
	for _, bid := range betIDs {
		bet := kvGetBytes(betKey(bid))
		if bet == nil || len(bet) < betSize {
			continue
		}
		stake := binary.LittleEndian.Uint64(bet[8:16])
		totalStake += stake
		switch bet[16] {
		case statusActive, statusCashoutReq:
			active++
		case statusSettled:
			cashed++
		}
	}
	return
}

func getBettorAddr(betID uint64) string {
	buf := make([]byte, maxAddrBufSize)
	n := host_get_bettor(betID, uint32(uintptr(unsafe.Pointer(&buf[0]))))
	if n == 0 || n > maxAddrBufSize {
		return ""
	}
	return string(buf[:n])
}

func pushHistory(crashMult uint64) {
	old := kvGetBytes(keyHistory)
	newBuf := make([]byte, 8)
	binary.LittleEndian.PutUint64(newBuf, crashMult)
	if old == nil {
		kvSet(keyHistory, newBuf)
		return
	}
	maxBytes := maxHistory * 8
	combined := make([]byte, len(old)+8)
	copy(combined, newBuf)
	copy(combined[8:], old)
	if len(combined) > maxBytes {
		combined = combined[:maxBytes]
	}
	kvSet(keyHistory, combined)
}

// emitState emits the flat 9-field state event.
func emitState(phase string, round, multBP, tick, blocksLeft, players, active, cashed, stake uint64) {
	emitJSON("state",
		"phase", phase,
		"round", round,
		"mult_bp", multBP,
		"tick", tick,
		"blocks_left", blocksLeft,
		"players", players,
		"active", active,
		"cashed", cashed,
		"stake", stake,
	)
}

// ---------------------------------------------------------------------------
// Bet list helpers
// ---------------------------------------------------------------------------

func appendBetID(id uint64) {
	list := kvGetBytes(keyBetList)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, id)
	if list == nil {
		kvSet(keyBetList, buf)
	} else {
		newList := make([]byte, len(list)+8)
		copy(newList, list)
		copy(newList[len(list):], buf)
		kvSet(keyBetList, newList)
	}
}

func loadBetIDs() []uint64 {
	list := kvGetBytes(keyBetList)
	if list == nil {
		return nil
	}
	n := len(list) / 8
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		ids[i] = binary.LittleEndian.Uint64(list[i*8:])
	}
	return ids
}

func appendCashout(height, betID uint64) {
	key := cashoutKey(height)
	list := kvGetBytes(key)
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, betID)
	if list == nil {
		kvSet(key, buf)
	} else {
		newList := make([]byte, len(list)+8)
		copy(newList, list)
		copy(newList[len(list):], buf)
		kvSet(key, newList)
	}
}

func loadAndDeleteCashoutIDs(height uint64) []uint64 {
	key := cashoutKey(height)
	list := kvGetBytes(key)
	if list == nil {
		return nil
	}
	kvDelete(key)
	n := len(list) / 8
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		ids[i] = binary.LittleEndian.Uint64(list[i*8:])
	}
	return ids
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

func kvGetRound() []byte {
	v := kvGetBytes(keyRound)
	if v != nil && len(v) >= roundSize {
		return v
	}
	return nil
}

func kvGetOrInitCfg() []byte {
	cfg := kvGetBytes(keyCfg)
	if cfg != nil && len(cfg) >= cfgSize {
		return cfg
	}
	cfg = make([]byte, cfgSize)
	binary.LittleEndian.PutUint64(cfg[0:], 200)       // house_edge_bp
	binary.LittleEndian.PutUint64(cfg[8:], 350)        // tick_growth_bp
	binary.LittleEndian.PutUint64(cfg[16:], 1_000_000) // max_multiplier_bp = 100x
	binary.LittleEndian.PutUint64(cfg[24:], 0)         // max_ticks = unlimited
	binary.LittleEndian.PutUint64(cfg[32:], 16)        // join_window_blocks
	binary.LittleEndian.PutUint64(cfg[40:], 5)         // crashed_cooldown_blocks
	kvSet(keyCfg, cfg)
	return cfg
}

// ---------------------------------------------------------------------------
// Math
// ---------------------------------------------------------------------------

func safeMulDiv(a, b, c uint64) uint64 {
	if c == 0 {
		return 0
	}
	if a <= 0xFFFFFFFF || b <= 0xFFFFFFFF {
		return a * b / c
	}
	aH, aL := a>>32, a&0xFFFFFFFF
	bH, bL := b>>32, b&0xFFFFFFFF
	mid1 := aH * bL
	mid2 := aL * bH
	low := aL * bL
	high := aH * bH
	carry := uint64(0)
	midSum := mid1 + mid2
	if midSum < mid1 {
		carry = 1
	}
	high += carry
	high += midSum >> 32
	low += midSum << 32
	if low < midSum<<32 {
		high++
	}
	if high == 0 {
		return low / c
	}
	q := high / c
	r := high % c
	return q<<64 + (r<<64+low)/c
}

func getUniformProb(seed []byte, tickH uint64) float64 {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], tickH)
	data := make([]byte, len(seed)+8)
	copy(data, seed)
	copy(data[len(seed):], buf[:])
	sum := sha256sum(data)
	val := binary.BigEndian.Uint64(sum[0:8])
	den := float64(^uint64(0))
	x := float64(val) / den
	if x <= 0 {
		return math.SmallestNonzeroFloat64
	}
	return x
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
// query — returns current state + players + history
// ---------------------------------------------------------------------------

//export query
func query() *byte {
	round := kvGetRound()
	rn := getRoundNumber()

	phase := "unknown"
	if round != nil {
		switch round[24] {
		case phaseOpen:
			phase = "open"
		case phaseTick:
			phase = "tick"
		case phaseCrashed:
			phase = "crashed"
		}
	}

	multBP := uint64(10000)
	tick := uint64(0)
	blocksLeft := uint64(0)
	if round != nil {
		multBP = binary.LittleEndian.Uint64(round[0:8])
		tick = binary.LittleEndian.Uint64(round[8:16])
		if phase == "open" {
			waitBuf := kvGetBytes([]byte("wt"))
			if waitBuf != nil && len(waitBuf) >= 8 {
				blocksLeft = binary.LittleEndian.Uint64(waitBuf)
			}
		} else if phase == "crashed" {
			blocksLeft = binary.LittleEndian.Uint64(round[16:24])
		}
	}

	// Players.
	betIDs := loadBetIDs()
	playersBuf := make([]byte, 0, 512)
	playersBuf = append(playersBuf, '[')
	first := true
	for _, bid := range betIDs {
		bet := kvGetBytes(betKey(bid))
		if bet == nil || len(bet) < betSize {
			continue
		}
		if !first {
			playersBuf = append(playersBuf, ',')
		}
		first = false
		stake := binary.LittleEndian.Uint64(bet[8:16])
		status := "active"
		if bet[16] == statusSettled {
			status = "out"
		} else if bet[16] == statusCashoutReq {
			status = "cashout_pending"
		}
		addr := getBettorAddr(bid)
		playersBuf = append(playersBuf, '{')
		playersBuf = append(playersBuf, `"id":`...)
		playersBuf = appendUint(playersBuf, bid)
		playersBuf = append(playersBuf, `,"addr":"`...)
		playersBuf = append(playersBuf, addr...)
		playersBuf = append(playersBuf, '"')
		playersBuf = append(playersBuf, `,"stake":`...)
		playersBuf = appendUint(playersBuf, stake)
		playersBuf = append(playersBuf, `,"status":"`...)
		playersBuf = append(playersBuf, status...)
		playersBuf = append(playersBuf, '"', '}')
	}
	playersBuf = append(playersBuf, ']')

	// History.
	histBuf := kvGetBytes(keyHistory)
	histJSON := make([]byte, 0, 128)
	histJSON = append(histJSON, '[')
	if histBuf != nil {
		n := len(histBuf) / 8
		for i := 0; i < n; i++ {
			if i > 0 {
				histJSON = append(histJSON, ',')
			}
			v := binary.LittleEndian.Uint64(histBuf[i*8:])
			histJSON = appendUint(histJSON, v)
		}
	}
	histJSON = append(histJSON, ']')

	// Build response.
	out := make([]byte, 0, 1024)
	out = append(out, '{')
	out = append(out, `"round":`...)
	out = appendUint(out, rn)
	out = append(out, `,"phase":"`...)
	out = append(out, phase...)
	out = append(out, '"')
	out = append(out, `,"mult_bp":`...)
	out = appendUint(out, multBP)
	out = append(out, `,"tick":`...)
	out = appendUint(out, tick)
	out = append(out, `,"blocks_left":`...)
	out = appendUint(out, blocksLeft)
	out = append(out, `,"players":`...)
	out = append(out, playersBuf...)
	out = append(out, `,"history":`...)
	out = append(out, histJSON...)
	out = append(out, '}')

	result := make([]byte, 4+len(out))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(out)))
	copy(result[4:], out)
	return &result[0]
}

// ---------------------------------------------------------------------------
// info
// ---------------------------------------------------------------------------

//export info
func info() *byte {
	data := []byte(`{
		"name":"Crash",
		"engine":"crash",
		"mode":"v3",
		"house_edge_bp":200,
		"developer":"ExoHash",
		"description":"Multiplayer crash — rising multiplier with random crash point",
		"errors":{
			"place_bet":{
				"3":"Insufficient bankroll liquidity",
				"10":"Round not accepting bets — wait for next round",
				"11":"Already joined this round"
			},
			"bet_action":{
				"20":"Round not in tick phase — cannot cashout yet",
				"21":"Bet not active — already cashed out or settled"
			}
		}
	}`)
	result := make([]byte, 4+len(data))
	binary.LittleEndian.PutUint32(result[0:4], uint32(len(data)))
	copy(result[4:], data)
	return &result[0]
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
