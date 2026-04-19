# ui

Reference casino frontend — **snapshot of [exohash-play](https://github.com/exohash-labs/exohash-play)** as of the last devkit release. Next.js 16 + Tailwind + TypeScript. Talks to the BFF over HTTP + SSE; signs authz grants with cosmjs.

Use this as:

1. **A runnable demo** — `npm run build && npm start` against `bffsim` and you have the full experience
2. **A reference implementation** — copy patterns (wallet creation, SSE replay/live boundary, relay calls, error humanization) into your own frontend
3. **A fork target** — rebrand the aesthetic, add games, ship it

## Run against bffsim (default)

```bash
# Prerequisites: bffsim running on :3100
# (from repo root: go run ./cmd/bffsim)

npm install
npm run build
npm start -- --port 3001    # ~10ms page loads, static-prerendered
```

Then open [http://localhost:3001/dice](http://localhost:3001/dice).

For hot reload during development:

```bash
npm run dev -- --port 3001   # Turbopack, slower but watches changes
```

## Run against a real BFF

Edit `.env`:

```env
BFF_URL=https://your-bff.example.com
CHAIN_API_URL=https://your-cosmos-lcd.example.com
NEXT_PUBLIC_BFF_DIRECT_URL=https://your-bff.example.com
NEXT_PUBLIC_BANKROLL_ID=1
NEXT_PUBLIC_SCAN_URL=https://scan.exohash.io
```

`NEXT_PUBLIC_*` bake in at build time — rebuild after changes.

## Structure

```
app/               Next.js app router — one page per game
  dice/page.tsx    Dice game page
  crash/page.tsx   Crash game page
  mines/page.tsx   Mines game page
  page.tsx         Landing
components/        Shared UI (wallet modal, balance, bet feed, etc.)
contexts/          React context providers (wallet, SSE stream)
lib/
  bff.ts           HTTP client for BFF endpoints
  signer.ts        cosmjs-based tx signing for authz grants
  wallet.ts        In-browser mnemonic wallet
  crypto.ts        Key derivation
  useStream.ts     SSE subscription hook with replay/live handling
  useBetFeed.ts    Live bet scroll feed
  useWaitForBet.ts Resolves a pending bet by matching address in SSE
  format.ts        uusdc ↔ USDC conversion, address shortening
  types.ts         Shared types
next.config.ts     Proxies /api/bff/* → BFF, /api/chain/* → CHAIN_API_URL
```

## How signing works

- **Wallet:** 24-word mnemonic generated + stored in localStorage (dev-only — never ship this pattern to production without a proper wallet integration)
- **Authz grant:** player signs a one-time `MsgGrant` giving the BFF relay key permission to place bets on their behalf — eliminates per-bet signing friction
- **Bet placement:** after the grant, `POST /relay/place-bet` is relay-signed; no client signing per bet

Against `bffsim`, the Cosmos REST endpoints (`/cosmos/...`) are mocked — the signer goes
through the same code paths but the broadcast is a no-op.
