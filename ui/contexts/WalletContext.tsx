"use client";

import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  type ReactNode,
} from "react";
import { DirectSecp256k1HdWallet } from "@cosmjs/proto-signing";
import { encrypt, decrypt } from "@/lib/crypto";
import { ensureRelayGrant } from "@/lib/signer";

const EXOHASH_PREFIX = "exo";
const STORAGE_KEY = "exohash-wallet";
const SESSION_KEY = "exohash-session"; // password cached for tab lifetime (like Gmail)

// --- Types ---

export type WalletStatus = "none" | "locked" | "unlocked";

interface StoredWallet {
  address: string;
  encryptedMnemonic: string;
}

interface WalletState {
  ready: boolean;
  status: WalletStatus;
  address: string | null;
  wallet: DirectSecp256k1HdWallet | null;
  // Actions
  create: (password: string) => Promise<string>; // returns mnemonic
  importWallet: (mnemonic: string, password: string) => Promise<void>;
  unlock: (password: string) => Promise<void>;
  lock: () => void;
  clear: () => void;
  // Modal
  showModal: boolean;
  openModal: (...args: any[]) => void;
  closeModal: () => void;
  pendingRedirect: string | null;
}

const WalletContext = createContext<WalletState | null>(null);

export function useWallet(): WalletState {
  const ctx = useContext(WalletContext);
  if (!ctx) throw new Error("useWallet must be inside WalletProvider");
  return ctx;
}

// --- Provider ---

export function WalletProvider({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);
  const [status, setStatus] = useState<WalletStatus>("none");
  const [address, setAddress] = useState<string | null>(null);
  const [wallet, setWallet] = useState<DirectSecp256k1HdWallet | null>(null);
  const [showModal, setShowModal] = useState(false);

  // Check localStorage on mount — auto-unlock if session password is cached.
  useEffect(() => {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) { setReady(true); return; }
    try {
      const stored: StoredWallet = JSON.parse(raw);
      if (!stored.address || !stored.encryptedMnemonic) { setReady(true); return; }
      setAddress(stored.address);

      // Try auto-unlock from sessionStorage (survives reload, cleared on tab close).
      const cachedPw = sessionStorage.getItem(SESSION_KEY);
      if (cachedPw) {
        (async () => {
          try {
            const mnemonic = decrypt(stored.encryptedMnemonic, cachedPw);
            const hdWallet = await DirectSecp256k1HdWallet.fromMnemonic(mnemonic, {
              prefix: EXOHASH_PREFIX,
            });
            const [account] = await hdWallet.getAccounts();
            if (account.address === stored.address) {
              setWallet(hdWallet);
              setStatus("unlocked");
              // Proactively ensure relay grant exists (handles chain restart)
              ensureRelayGrant(hdWallet, account.address).catch(() => {});
              setReady(true);
              return;
            }
          } catch {
            sessionStorage.removeItem(SESSION_KEY);
          }
          setStatus("locked");
          setReady(true);
        })();
      } else {
        setStatus("locked");
        setReady(true);
      }
    } catch {
      // corrupt data, ignore
      setReady(true);
    }
  }, []);

  const saveToStorage = useCallback(
    (addr: string, encMnemonic: string) => {
      const data: StoredWallet = {
        address: addr,
        encryptedMnemonic: encMnemonic,
      };
      localStorage.setItem(STORAGE_KEY, JSON.stringify(data));
    },
    []
  );

  // Create new wallet
  const create = useCallback(
    async (password: string): Promise<string> => {
      const hdWallet = await DirectSecp256k1HdWallet.generate(24, {
        prefix: EXOHASH_PREFIX,
      });
      const [account] = await hdWallet.getAccounts();
      const encMnemonic = encrypt(hdWallet.mnemonic, password);
      saveToStorage(account.address, encMnemonic);
      sessionStorage.setItem(SESSION_KEY, password);
      setAddress(account.address);
      setWallet(hdWallet);
      setStatus("unlocked");
      ensureRelayGrant(hdWallet, account.address).catch(() => {});
      return hdWallet.mnemonic;
    },
    [saveToStorage]
  );

  // Import existing wallet from mnemonic
  const importWallet = useCallback(
    async (mnemonic: string, password: string): Promise<void> => {
      const hdWallet = await DirectSecp256k1HdWallet.fromMnemonic(mnemonic.trim(), {
        prefix: EXOHASH_PREFIX,
      });
      const [account] = await hdWallet.getAccounts();
      const encMnemonic = encrypt(mnemonic.trim(), password);
      saveToStorage(account.address, encMnemonic);
      sessionStorage.setItem(SESSION_KEY, password);
      setAddress(account.address);
      setWallet(hdWallet);
      setStatus("unlocked");
      ensureRelayGrant(hdWallet, account.address).catch(() => {});
    },
    [saveToStorage]
  );

  // Unlock with password
  const unlock = useCallback(async (password: string): Promise<void> => {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) throw new Error("No wallet found");
    const stored: StoredWallet = JSON.parse(raw);
    const mnemonic = decrypt(stored.encryptedMnemonic, password);
    const hdWallet = await DirectSecp256k1HdWallet.fromMnemonic(mnemonic, {
      prefix: EXOHASH_PREFIX,
    });
    const [account] = await hdWallet.getAccounts();
    if (account.address !== stored.address) {
      throw new Error("Address mismatch — wrong password or corrupt data");
    }
    sessionStorage.setItem(SESSION_KEY, password);
    setWallet(hdWallet);
    setStatus("unlocked");
    ensureRelayGrant(hdWallet, account.address).catch(() => {});
  }, []);

  // Lock — clear wallet from memory, keep encrypted in storage
  const lock = useCallback(() => {
    sessionStorage.removeItem(SESSION_KEY);
    setWallet(null);
    setStatus("locked");
  }, []);

  // Clear — remove everything
  const clear = useCallback(() => {
    localStorage.removeItem(STORAGE_KEY);
    sessionStorage.removeItem(SESSION_KEY);
    setWallet(null);
    setAddress(null);
    setStatus("none");
  }, []);

  const [pendingRedirect, setPendingRedirect] = useState<string | null>(null);
  const openModal = useCallback((redirectTo?: string) => {
    if (typeof redirectTo === "string") setPendingRedirect(redirectTo);
    setShowModal(true);
  }, []);
  const closeModal = useCallback(() => {
    setShowModal(false);
    setPendingRedirect(null);
  }, []);

  return (
    <WalletContext.Provider
      value={{
        ready,
        status,
        address,
        wallet,
        create,
        importWallet,
        unlock,
        lock,
        clear,
        showModal,
        openModal,
        closeModal,
        pendingRedirect,
      }}
    >
      {children}
    </WalletContext.Provider>
  );
}
