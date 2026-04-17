#!/bin/bash
# Stress test: acts as a malicious/chaotic player against bffsim (:4000)
set -o pipefail

BFF="http://localhost:4000"
PASS=0
FAIL=0

ok() {
    echo "  ✓ $1"
    PASS=$((PASS+1))
}
fail() {
    echo "  ✗ $1"
    FAIL=$((FAIL+1))
}

post() { curl -s -X POST "$BFF$1" -H "Content-Type: application/json" -d "$2" 2>&1; }
get() { curl -s "$BFF$1" 2>&1; }
fund() { post "/faucet/request" "{\"address\":\"$1\"}" > /dev/null; }

place_bet() {
    post "/relay/place-bet" "{\"address\":\"$1\",\"bankrollId\":1,\"calculatorId\":$2,\"stake\":\"$3\",\"params\":$4}"
}
bet_action() {
    post "/relay/bet-action" "{\"address\":\"$1\",\"betId\":$2,\"action\":$3}"
}

jq_field() { echo "$1" | python3 -c "import sys,json; print(json.load(sys.stdin).get('$2',''))"; }
has_err() { echo "$1" | python3 -c "import sys,json; d=json.load(sys.stdin); exit(0 if 'error' in d else 1)"; }
bet_status() { get "/bet/$1/state" | python3 -c "import sys,json; print(json.load(sys.stdin).get('status','?'))"; }
balance() { get "/account/$1/balance" | python3 -c "import sys,json; print(json.load(sys.stdin).get('usdc','0'))"; }

expect_error() {
    local name="$1" resp="$2"
    if has_err "$resp"; then ok "$name"; else fail "$name — expected error, got: $(echo "$resp" | head -c 80)"; fi
}
expect_ok() {
    local name="$1" resp="$2"
    if has_err "$resp"; then fail "$name — got error: $(jq_field "$resp" error)"; else ok "$name"; fi
}

echo ""
echo "============================================"
echo "  STRESS TEST: Malicious Player Scenarios"
echo "============================================"

# ---------------------------------------------------------------
echo ""
echo "--- DICE ABUSE ---"
fund "dice_abuser"

R=$(place_bet "dice_abuser" 1 "0" "[2,136,19,0,0,0,0,0,0]")
expect_error "zero stake rejected" "$R"

R=$(place_bet "dice_abuser" 1 "999999999999" "[2,136,19,0,0,0,0,0,0]")
expect_error "stake > balance rejected" "$R"

R=$(place_bet "dice_abuser" 1 "1000000" "[2,0,0,0,0,0,0,0,0]")
expect_error "0% chance rejected" "$R"

# 9900bp = 99%, encoded LE: 172,38
R=$(place_bet "dice_abuser" 1 "1000000" "[2,172,38,0,0,0,0,0,0]")
expect_error "99% chance rejected" "$R"

R=$(place_bet "dice_abuser" 1 "1000000" "[]")
expect_error "empty params rejected" "$R"

# Valid bet
R=$(place_bet "dice_abuser" 1 "1000000" "[2,136,19,0,0,0,0,0,0]")
expect_ok "valid 50% bet accepted" "$R"
BID=$(jq_field "$R" betId)
sleep 2
S=$(bet_status "$BID")
if [ "$S" = "settled" ]; then ok "dice settles after 1 block"; else fail "dice not settled: $S"; fi

# Rapid fire
echo "[Dice] Rapid-fire 10 bets"
OK_COUNT=0
for i in $(seq 1 10); do
    R=$(place_bet "dice_abuser" 1 "100000" "[2,136,19,0,0,0,0,0,0]")
    has_err "$R" || OK_COUNT=$((OK_COUNT+1))
done
if [ $OK_COUNT -ge 8 ]; then ok "rapid fire: $OK_COUNT/10 accepted"; else fail "rapid fire: only $OK_COUNT/10"; fi

# ---------------------------------------------------------------
echo ""
echo "--- MINES ABUSE ---"
fund "mines_a"

R=$(place_bet "mines_a" 3 "1000000" "[0]")
expect_error "0 mines rejected" "$R"

R=$(place_bet "mines_a" 3 "1000000" "[14]")
expect_error "14 mines rejected" "$R"

# Start + reveal + duplicate reveal
R=$(place_bet "mines_a" 3 "1000000" "[3]")
expect_ok "mines started" "$R"
BID=$(jq_field "$R" betId)
sleep 1
R=$(bet_action "mines_a" "$BID" "[1,5]")
expect_ok "first reveal accepted" "$R"
sleep 1
R=$(bet_action "mines_a" "$BID" "[1,5]")
expect_error "duplicate tile rejected" "$R"

# Cashout before any reveal
fund "mines_b"
R=$(place_bet "mines_b" 3 "1000000" "[3]")
BID2=$(jq_field "$R" betId)
sleep 1
R=$(bet_action "mines_b" "$BID2" "[2]")
expect_error "cashout-before-reveal rejected" "$R"

# Tile out of range
fund "mines_c"
R=$(place_bet "mines_c" 3 "1000000" "[1]")
BID3=$(jq_field "$R" betId)
sleep 1
R=$(bet_action "mines_c" "$BID3" "[1,25]")
expect_error "tile 25 (out of range) rejected" "$R"

# Impersonation
R=$(bet_action "impersonator" "$BID3" "[1,0]")
expect_error "impersonation rejected" "$R"

# Invalid action type
R=$(bet_action "mines_c" "$BID3" "[99]")
expect_error "invalid action type rejected" "$R"

# Reveal during waiting-for-RNG
fund "mines_d"
R=$(place_bet "mines_d" 3 "1000000" "[1]")
BID4=$(jq_field "$R" betId)
sleep 1
bet_action "mines_d" "$BID4" "[1,0]" > /dev/null
R=$(bet_action "mines_d" "$BID4" "[1,1]")
expect_error "reveal-during-RNG rejected" "$R"

# Timeout test (40 blocks @ 500ms = 20s, wait 25s)
echo "[Mines] Timeout test (25s wait)..."
fund "mines_timeout"
R=$(place_bet "mines_timeout" 3 "500000" "[1]")
BID_TO=$(jq_field "$R" betId)
sleep 25
S=$(bet_status "$BID_TO")
if [ "$S" = "refunded" ]; then ok "timeout → refunded"; elif [ "$S" = "settled" ]; then ok "timeout → settled (auto-cashout)"; else fail "timeout: status=$S (expected refunded/settled)"; fi

# ---------------------------------------------------------------
echo ""
echo "--- CRASH ABUSE ---"
fund "crash_a"

# Action on nonexistent bet
R=$(bet_action "crash_a" 99999999 "[1]")
expect_error "nonexistent bet rejected" "$R"

# Join crash
echo "[Crash] Joining (retrying until open)..."
CRASH_BID=0
for i in $(seq 1 80); do
    R=$(place_bet "crash_a" 2 "1000000" "[]")
    if ! has_err "$R"; then
        CRASH_BID=$(jq_field "$R" betId)
        break
    fi
    sleep 0.3
done
if [ "$CRASH_BID" -gt 0 ] 2>/dev/null; then ok "joined crash (betId=$CRASH_BID)"; else fail "could not join crash"; fi

# Double join same round
R=$(place_bet "crash_a" 2 "1000000" "[]")
expect_error "double join same round rejected" "$R"

# Cashout during open
R=$(bet_action "crash_a" "$CRASH_BID" "[1]")
expect_error "cashout during open rejected" "$R"

# Wait for settlement (poll)
echo "[Crash] Waiting for settlement (polling)..."
for i in $(seq 1 40); do
    S=$(bet_status "$CRASH_BID")
    [ "$S" = "settled" ] && break
    sleep 1
done
S=$(bet_status "$CRASH_BID")
if [ "$S" = "settled" ]; then ok "crash bet settled"; else fail "crash not settled: $S"; fi

# Action after settlement
R=$(bet_action "crash_a" "$CRASH_BID" "[1]")
expect_error "post-settlement action rejected" "$R"

# ---------------------------------------------------------------
echo ""
echo "--- CROSS-GAME ABUSE ---"
fund "cross_a"

# Mines action on dice bet
R=$(place_bet "cross_a" 1 "1000000" "[2,136,19,0,0,0,0,0,0]")
DBID=$(jq_field "$R" betId)
R=$(bet_action "cross_a" "$DBID" "[1,5]")
expect_error "mines action on dice bet rejected" "$R"

R=$(place_bet "cross_a" 99 "1000000" "[]")
expect_error "unknown calcId rejected" "$R"

R=$(post "/relay/place-bet" "{\"address\":\"cross_a\",\"bankrollId\":99,\"calculatorId\":1,\"stake\":\"1000000\",\"params\":[2,136,19,0,0,0,0,0,0]}")
expect_error "unknown bankrollId rejected" "$R"

R=$(place_bet "nobody_unfunded" 1 "1000000" "[2,136,19,0,0,0,0,0,0]")
expect_error "unfunded address rejected" "$R"

# Faucet spam
for i in $(seq 1 10); do fund "faucet_spam"; done
BAL=$(balance "faucet_spam")
if [ "$BAL" = "1000000000" ]; then ok "faucet spam: 10x = 1B uusdc"; else fail "faucet spam: balance=$BAL"; fi

# ---------------------------------------------------------------
echo ""
echo "--- SSE VERIFICATION ---"

curl -s -N "$BFF/stream" > /tmp/stress_sse.txt &
SSE_PID=$!
sleep 8
kill $SSE_PID 2>/dev/null
wait $SSE_PID 2>/dev/null

CONNECTED=$(head -1 /tmp/stress_sse.txt | grep -c "connected")
if [ "$CONNECTED" -gt 0 ]; then ok "SSE connected signal"; else fail "SSE no connected signal"; fi

REPLAY_END=$(grep -c '"replay":false' /tmp/stress_sse.txt)
if [ "$REPLAY_END" -ge 1 ]; then ok "SSE replay end marker"; else fail "SSE no replay end"; fi

LIVE_LINES=$(sed -n '/replay.:false/,$ p' /tmp/stress_sse.txt | wc -l)
if [ "$LIVE_LINES" -gt 2 ]; then ok "SSE live events flowing ($LIVE_LINES lines)"; else fail "SSE no live events"; fi

echo ""
echo "============================================"
echo "  Results: $PASS passed, $FAIL failed"
echo "============================================"
echo ""
[ $FAIL -eq 0 ] && exit 0 || exit 1
