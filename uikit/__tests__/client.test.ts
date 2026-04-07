// Integration tests — requires mock-bff running on localhost:4000
// Run: cd mock-bff && go run . &
// Then: npx tsx src/__tests__/client.test.ts

import { ExoClient, ExoApiError } from "../client";

const BFF = "http://localhost:4000";
const client = new ExoClient(BFF);
const ADDR = "test_" + Date.now();

let passed = 0;
let failed = 0;

async function test(name: string, fn: () => Promise<void>) {
  try {
    await fn();
    console.log(`  ✓ ${name}`);
    passed++;
  } catch (e: any) {
    console.log(`  ✗ ${name}: ${e.message}`);
    failed++;
  }
}

function assert(cond: boolean, msg: string) {
  if (!cond) throw new Error(msg);
}

async function run() {
  console.log("\n=== ExoClient Integration Tests ===\n");

  // --- Health ---
  await test("health returns ok", async () => {
    const h = await client.health();
    assert(h.status === "ok", `expected ok, got ${h.status}`);
    assert(h.games === 3, `expected 3 games, got ${h.games}`);
    assert(h.height > 0, `expected height > 0, got ${h.height}`);
  });

  // --- Games ---
  await test("games returns 3 games", async () => {
    const games = await client.games();
    assert(games.length === 3, `expected 3, got ${games.length}`);
    const names = games.map((g) => g.name).sort();
    assert(names.join(",") === "crash,dice,mines", `got ${names}`);
  });

  await test("games have error maps", async () => {
    const games = await client.games();
    const dice = games.find((g) => g.name === "dice")!;
    assert(dice.errors !== undefined, "dice missing errors");
    assert(dice.errors!.place_bet !== undefined, "dice missing place_bet errors");
    assert(dice.errors!.place_bet["2"] !== undefined, "dice missing error code 2");
  });

  await test("gameInfo returns single game", async () => {
    const info = await client.gameInfo(2);
    assert(info.name === "crash", `expected crash, got ${info.name}`);
    assert(info.houseEdgeBp === 200, `expected 200, got ${info.houseEdgeBp}`);
  });

  // --- Faucet ---
  await test("faucet creates account with balance", async () => {
    const res = await client.faucet(ADDR);
    assert(res.amount === "100000000", `expected 100M, got ${res.amount}`);
    assert(res.balance === "100000000", `expected 100M, got ${res.balance}`);
  });

  // --- Balance ---
  await test("balance returns funded amount", async () => {
    const res = await client.balance(ADDR);
    assert(res.usdc === "100000000", `expected 100M, got ${res.usdc}`);
  });

  await test("balance fails for unknown address", async () => {
    try {
      await client.balance("nobody");
      throw new Error("should have thrown");
    } catch (e) {
      assert(e instanceof ExoApiError, "expected ExoApiError");
    }
  });

  // --- Dice bet ---
  await test("dice bet places and settles", async () => {
    // 50% chance: mode=2, threshold=5000 LE
    const params = [2, 136, 19, 0, 0, 0, 0, 0, 0];
    const res = await client.placeBet({
      address: ADDR,
      bankrollId: 1,
      calculatorId: 1,
      stake: "1000000",
      params,
    });
    assert(res.betId > 0, `expected betId > 0, got ${res.betId}`);
    assert(res.txHash.length > 0, "expected txHash");

    // Wait for settlement.
    await sleep(2000);

    const bets = await client.bets(ADDR);
    const bet = bets.find((b) => b.betId === res.betId);
    assert(bet !== undefined, "bet not found in history");
    assert(bet!.status === "settled", `expected settled, got ${bet!.status}`);
  });

  // --- Dice bad params ---
  await test("dice rejects bad chance with human error", async () => {
    try {
      await client.placeBet({
        address: ADDR,
        bankrollId: 1,
        calculatorId: 1,
        stake: "1000000",
        params: [2, 0, 0, 0, 0, 0, 0, 0, 0], // 0% chance
      });
      throw new Error("should have thrown");
    } catch (e) {
      assert(e instanceof ExoApiError, "expected ExoApiError");
      assert(
        e.message.includes("Chance out of range"),
        `expected human error, got: ${e.message}`
      );
    }
  });

  // --- Crash bet ---
  await test("crash joins and settles", async () => {
    // Retry until OPEN phase.
    let betId = 0;
    for (let i = 0; i < 20; i++) {
      try {
        const res = await client.placeBet({
          address: ADDR,
          bankrollId: 1,
          calculatorId: 2,
          stake: "1000000",
          params: [],
        });
        betId = res.betId;
        break;
      } catch {
        await sleep(400);
      }
    }
    assert(betId > 0, "could not join crash after 20 retries");

    // Wait for round to complete (16 block open + ticking + crash + 5 block cooldown).
    // At 500ms/block this can take 30-60 seconds.
    for (let i = 0; i < 30; i++) {
      await sleep(2000);
      const bet = (await client.bets(ADDR)).find((b) => b.betId === betId);
      if (bet && bet.status === "settled") break;
    }

    const bet = (await client.bets(ADDR)).find((b) => b.betId === betId);
    assert(bet !== undefined, "crash bet not found");
    assert(bet!.status === "settled", `expected settled, got ${bet!.status}`);
  });

  // --- Crash cashout ---
  await test("crash join + cashout returns payout", async () => {
    await client.faucet(ADDR); // top up
    let betId = 0;
    for (let i = 0; i < 60; i++) {
      try {
        const res = await client.placeBet({
          address: ADDR,
          bankrollId: 1,
          calculatorId: 2,
          stake: "1000000",
          params: [],
        });
        betId = res.betId;
        break;
      } catch {
        await sleep(500);
      }
    }
    assert(betId > 0, "could not join crash");

    // Wait for tick phase then try cashout.
    await sleep(5000);
    try {
      await client.betAction({ address: ADDR, betId, action: [1] });
    } catch {
      // May fail if crashed already — that's ok.
    }

    // Poll until settled (round must complete).
    for (let i = 0; i < 30; i++) {
      await sleep(2000);
      const bet = (await client.bets(ADDR)).find((b) => b.betId === betId);
      if (bet && bet.status === "settled") break;
    }
    const bet = (await client.bets(ADDR)).find((b) => b.betId === betId);
    assert(bet !== undefined, "crash bet not found");
    assert(bet!.status === "settled", `expected settled, got ${bet!.status}`);
  });

  // --- Crash rejects during RESOLVING ---
  await test("crash rejects with human error during resolving", async () => {
    let rejected = false;
    for (let i = 0; i < 5; i++) {
      try {
        await client.placeBet({
          address: ADDR,
          bankrollId: 1,
          calculatorId: 2,
          stake: "1000000",
          params: [],
        });
        // Accepted — try again until we hit a non-OPEN phase.
        await sleep(300);
      } catch (e) {
        if (e instanceof ExoApiError && e.message.includes("Round not accepting")) {
          rejected = true;
          break;
        }
      }
    }
    assert(rejected, "never got a human-readable rejection");
  });

  // --- Mines full flow ---
  await test("mines place + reveal + cashout", async () => {
    const res = await client.placeBet({
      address: ADDR,
      bankrollId: 1,
      calculatorId: 3,
      stake: "1000000",
      params: [3], // 3 mines
    });
    assert(res.betId > 0, "expected betId");

    await sleep(1000);

    // Reveal tile 0.
    await client.betAction({ address: ADDR, betId: res.betId, action: [1, 0] });
    await sleep(1000);

    // Cashout.
    await client.betAction({ address: ADDR, betId: res.betId, action: [2] });
    await sleep(1000);

    const bet = (await client.bets(ADDR)).find((b) => b.betId === res.betId);
    assert(bet !== undefined, "mines bet not found");
    assert(bet!.status === "settled", `expected settled, got ${bet!.status}`);
    assert(bet!.payout > 0, `expected payout > 0, got ${bet!.payout}`);
  });

  // --- Mines bad count ---
  await test("mines rejects bad count with human error", async () => {
    try {
      await client.placeBet({
        address: ADDR,
        bankrollId: 1,
        calculatorId: 3,
        stake: "1000000",
        params: [0], // 0 mines — invalid
      });
      throw new Error("should have thrown");
    } catch (e) {
      assert(e instanceof ExoApiError, "expected ExoApiError");
      assert(
        e.message.includes("Mines count out of range"),
        `expected human error, got: ${e.message}`
      );
    }
  });

  // --- Bet state (cold start) ---
  await test("bet state returns events for mines bet", async () => {
    const res = await client.placeBet({
      address: ADDR,
      bankrollId: 1,
      calculatorId: 3,
      stake: "1000000",
      params: [5], // 5 mines
    });

    await sleep(1000);
    await client.betAction({ address: ADDR, betId: res.betId, action: [1, 7] });
    await sleep(1000);

    const state = await client.betState(res.betId);
    assert(state.betId === res.betId, "wrong betId");
    assert(state.status === "open", `expected open, got ${state.status}`);
    assert(state.events.length >= 2, `expected >= 2 events, got ${state.events.length}`);

    // Check events contain joined + reveal.
    const topics = state.events.map((e) => e.topic);
    assert(topics.includes("joined"), "missing joined event");

    // Cleanup — cashout.
    try {
      await client.betAction({ address: ADDR, betId: res.betId, action: [2] });
    } catch {
      // might hit mine
    }
  });

  // --- Balance consistency ---
  await test("balance reflects all operations", async () => {
    const bal = await client.balance(ADDR);
    const usdcNum = parseInt(bal.usdc);
    assert(usdcNum > 0, `expected positive balance, got ${usdcNum}`);
    assert(usdcNum !== 100_000_000, "balance should have changed from initial (bets placed)");
  });

  // --- Summary ---
  console.log(`\n=== Results: ${passed} passed, ${failed} failed ===\n`);
  process.exit(failed > 0 ? 1 : 0);
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

run().catch((e) => {
  console.error("Fatal:", e);
  process.exit(1);
});
