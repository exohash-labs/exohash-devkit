/**
 * Transaction signing and broadcasting for Exohash.
 * Uses cosmjs SigningStargateClient with proper protobuf message types.
 */

import { DirectSecp256k1HdWallet, Registry } from "@cosmjs/proto-signing";
import { TxRaw } from "cosmjs-types/cosmos/tx/v1beta1/tx";
import { SigningStargateClient } from "@cosmjs/stargate";
import { toBase64 } from "@cosmjs/encoding";
import { MsgGrant } from "cosmjs-types/cosmos/authz/v1beta1/tx";
import { GenericAuthorization } from "cosmjs-types/cosmos/authz/v1beta1/authz";
import { bff } from "./bff";

const MSGGRANT_URL = "/cosmos.authz.v1beta1.MsgGrant";

function createRegistry(): Registry {
  const registry = new Registry();
  registry.register(MSGGRANT_URL, MsgGrant as any);
  registry.register("/cosmos.authz.v1beta1.GenericAuthorization", GenericAuthorization as any);
  return registry;
}

/** Fetch account info from chain API (via Next.js proxy). */
async function getAccountInfo(address: string): Promise<{ accountNumber: number; sequence: number; chainId: string }> {
  const chainBase = typeof window !== "undefined" ? "/api/chain" : (process.env.CHAIN_API_URL || "http://localhost:1317");

  // Get account number + sequence
  const accResp = await fetch(`${chainBase}/cosmos/auth/v1beta1/accounts/${address}`);
  if (!accResp.ok) throw new Error("Account not found on chain — request faucet first");
  const accData = await accResp.json();
  const acc = accData.account;

  // Get chain ID from node info
  const nodeResp = await fetch(`${chainBase}/cosmos/base/tendermint/v1beta1/node_info`);
  if (!nodeResp.ok) throw new Error("Failed to fetch node info");
  const nodeData = await nodeResp.json();

  return {
    accountNumber: Number(acc.account_number),
    sequence: Number(acc.sequence),
    chainId: nodeData.default_node_info?.network || "exohash-solo-1",
  };
}

/** Broadcast signed TX bytes via chain API. */
async function broadcastTx(txBytes: Uint8Array): Promise<any> {
  const chainBase = typeof window !== "undefined" ? "/api/chain" : (process.env.CHAIN_API_URL || "http://localhost:1317");
  const body = JSON.stringify({
    tx_bytes: toBase64(txBytes),
    mode: "BROADCAST_MODE_SYNC",
  });
  const resp = await fetch(`${chainBase}/cosmos/tx/v1beta1/txs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`Broadcast failed: ${text}`);
  }
  return resp.json();
}

const FEE = { amount: [{ denom: "uusdc", amount: "0" }], gas: "500000" };

// --- Authz Grant ---

const GRANT_MSG_TYPES = [
  "/house.types.MsgPlaceBet",
  "/house.types.MsgBetAction",
];

/**
 * grantRelay sends MsgGrant for all game message types to the relay address.
 * Called once after wallet creation / faucet.
 */
export async function grantRelay(
  wallet: DirectSecp256k1HdWallet,
  address: string,
  relayAddress: string
): Promise<any> {
  const { accountNumber, sequence, chainId } = await getAccountInfo(address);
  const registry = createRegistry();

  const msgs = GRANT_MSG_TYPES.map(msgType => ({
    typeUrl: MSGGRANT_URL,
    value: MsgGrant.fromPartial({
      granter: address,
      grantee: relayAddress,
      grant: {
        authorization: {
          typeUrl: "/cosmos.authz.v1beta1.GenericAuthorization",
          value: GenericAuthorization.encode(
            GenericAuthorization.fromPartial({ msg: msgType })
          ).finish(),
        },
      },
    }),
  }));

  // Sign offline (no RPC connection needed).
  const client = await SigningStargateClient.offline(wallet, { registry });
  const signed = await client.sign(address, msgs, FEE, "", {
    accountNumber,
    sequence,
    chainId,
  });
  const txBytes = TxRaw.encode(signed).finish();

  const resp = await broadcastTx(txBytes);
  // Cosmos REST API wraps response in tx_response
  const txResp = resp.tx_response || resp;
  if (txResp.code && txResp.code !== 0) {
    throw new Error(`Grant TX failed (code ${txResp.code}): ${txResp.raw_log || txResp.log || "unknown"}`);
  }
  return { code: 0, txhash: txResp.txhash, rawLog: txResp.raw_log };
}

/**
 * checkRelayGrant queries if the player has an active grant for the relay.
 * Uses BFF proxy to avoid direct chain access.
 */
export async function checkRelayGrant(playerAddr: string, relayAddr: string): Promise<boolean> {
  try {
    const chainBase = typeof window !== "undefined" ? "/api/chain" : (process.env.CHAIN_API_URL || "http://localhost:1317");
    const url = `${chainBase}/cosmos/authz/v1beta1/grants?granter=${playerAddr}&grantee=${relayAddr}&msg_type_url=${encodeURIComponent("/house.types.MsgPlaceBet")}`;
    const resp = await fetch(url);
    if (!resp.ok) return false;
    const data = await resp.json();
    return data.grants && data.grants.length > 0;
  } catch {
    return false;
  }
}

/**
 * ensureRelayGrant checks if the relay grant exists and creates it if missing.
 * Safe to call repeatedly — no-ops if grant already active.
 */
export async function ensureRelayGrant(
  wallet: DirectSecp256k1HdWallet,
  address: string
): Promise<void> {
  try {
    const relayInfo = await bff.relayInfo();
    if (!relayInfo.enabled || !relayInfo.relayAddress) return;
    const hasGrant = await checkRelayGrant(address, relayInfo.relayAddress);
    if (hasGrant) return;
    await grantRelay(wallet, address, relayInfo.relayAddress);
  } catch (e) {
  }
}

/** Poll for bet result until DONE or timeout */
export async function waitForBetResult(
  betId: number,
  timeoutMs = 15000,
  pollMs = 500
): Promise<any> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const bet = await bff.bet(betId);
      if (bet.phase === "GAME_PHASE_DONE") return bet;
      if (bet.phase === "GAME_PHASE_WAITING_USER") return bet;
    } catch {
      // bet not found yet
    }
    await new Promise((r) => setTimeout(r, pollMs));
  }
  throw new Error("Bet settlement timeout");
}

