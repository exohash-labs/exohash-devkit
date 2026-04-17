/**
 * Simple AES-like encryption for mnemonic storage using cosmjs crypto.
 * Uses SHA-256 for key derivation + XOR cipher with HMAC integrity check.
 * Good enough for testnet wallet protection. Not production-grade.
 */

import { sha256 } from "@cosmjs/crypto";
import { toUtf8, fromUtf8, toBase64, fromBase64 } from "@cosmjs/encoding";

function deriveKeyStream(password: string, salt: Uint8Array, length: number): Uint8Array {
  const stream = new Uint8Array(length);
  let offset = 0;
  let counter = 0;
  while (offset < length) {
    const input = new Uint8Array([...salt, ...toUtf8(password), counter & 0xff, (counter >> 8) & 0xff]);
    const hash = sha256(input);
    const chunk = Math.min(hash.length, length - offset);
    stream.set(hash.slice(0, chunk), offset);
    offset += chunk;
    counter++;
  }
  return stream;
}

function hmac(key: Uint8Array, data: Uint8Array): Uint8Array {
  const block = 64;
  const opad = new Uint8Array(block).fill(0x5c);
  const ipad = new Uint8Array(block).fill(0x36);
  const k = key.length > block ? sha256(key) : key;
  for (let i = 0; i < k.length; i++) {
    opad[i] ^= k[i];
    ipad[i] ^= k[i];
  }
  const inner = sha256(new Uint8Array([...ipad, ...data]));
  return sha256(new Uint8Array([...opad, ...inner]));
}

export function encrypt(plaintext: string, password: string): string {
  const data = toUtf8(plaintext);
  // Generate random salt
  const salt = new Uint8Array(16);
  if (typeof window !== "undefined" && window.crypto) {
    window.crypto.getRandomValues(salt);
  } else {
    // Fallback for non-browser (shouldn't happen for encrypt)
    for (let i = 0; i < salt.length; i++) salt[i] = Math.floor(Math.random() * 256);
  }
  const keyStream = deriveKeyStream(password, salt, data.length);
  const encrypted = new Uint8Array(data.length);
  for (let i = 0; i < data.length; i++) {
    encrypted[i] = data[i] ^ keyStream[i];
  }
  const mac = hmac(sha256(toUtf8(password)), new Uint8Array([...salt, ...encrypted]));
  // Pack: salt(16) + mac(32) + encrypted
  const packed = new Uint8Array(16 + 32 + encrypted.length);
  packed.set(salt, 0);
  packed.set(mac, 16);
  packed.set(encrypted, 48);
  return toBase64(packed);
}

export function decrypt(encoded: string, password: string): string {
  const packed = fromBase64(encoded);
  const salt = packed.slice(0, 16);
  const storedMac = packed.slice(16, 48);
  const encrypted = packed.slice(48);
  // Verify MAC
  const expectedMac = hmac(sha256(toUtf8(password)), new Uint8Array([...salt, ...encrypted]));
  let macOk = true;
  for (let i = 0; i < 32; i++) {
    if (storedMac[i] !== expectedMac[i]) macOk = false;
  }
  if (!macOk) throw new Error("Wrong password");
  const keyStream = deriveKeyStream(password, salt, encrypted.length);
  const decrypted = new Uint8Array(encrypted.length);
  for (let i = 0; i < encrypted.length; i++) {
    decrypted[i] = encrypted[i] ^ keyStream[i];
  }
  return fromUtf8(decrypted);
}
