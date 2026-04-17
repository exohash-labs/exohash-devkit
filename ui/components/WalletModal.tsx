"use client";

import { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { useWallet } from "@/contexts/WalletContext";
import { bff, formatUSDC, getUSDCBalance } from "@/lib/bff";
import { grantRelay, checkRelayGrant, ensureRelayGrant } from "@/lib/signer";
import { X, Eye, EyeOff, Copy, Check, LogOut, Droplets, Trash2, Shield } from "lucide-react";

type Tab = "create" | "import" | "unlock";

export function WalletModal() {
  const router = useRouter();
  const {
    status,
    address,
    wallet,
    showModal,
    closeModal,
    create,
    importWallet,
    unlock,
    lock,
    clear,
    pendingRedirect,
  } = useWallet();

  const [tab, setTab] = useState<Tab>(status === "locked" ? "unlock" : "create");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [mnemonic, setMnemonic] = useState("");
  const [generatedMnemonic, setGeneratedMnemonic] = useState("");
  const [mnemonicSaved, setMnemonicSaved] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copiedAddr, setCopiedAddr] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [balance, setBalance] = useState("0");
  const [faucetLoading, setFaucetLoading] = useState(false);
  const [faucetMsg, setFaucetMsg] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deleteInput, setDeleteInput] = useState("");
  const [faucetStep, setFaucetStep] = useState(false);

  // Fetch balance when modal opens and wallet is unlocked.
  useEffect(() => {
    if (showModal && status === "unlocked" && address) {
      bff.balance(address).then(b => setBalance(getUSDCBalance(b.balances))).catch(() => {});
    }
  }, [showModal, status, address]);

  if (!showModal) return null;

  const reset = () => {
    setPassword("");
    setConfirmPassword("");
    setMnemonic("");
    setGeneratedMnemonic("");
    setMnemonicSaved(false);
    setError("");
    setLoading(false);
    setCopied(false);
    setCopiedAddr(false);
    setFaucetMsg("");
    setFaucetStep(false);
  };

  const handleClose = () => {
    reset();
    closeModal();
  };

  const handleCreate = async () => {
    if (password.length < 3) {
      setError("Password must be at least 3 characters");
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords don't match");
      return;
    }
    setError("");
    setLoading(true);
    try {
      const m = await create(password);
      setGeneratedMnemonic(m);
    } catch (e: any) {
      setError(e.message || "Failed to create wallet");
    }
    setLoading(false);
  };

  const handleImport = async () => {
    if (password.length < 3) {
      setError("Password must be at least 3 characters");
      return;
    }
    if (!mnemonic.trim()) {
      setError("Enter your seed phrase");
      return;
    }
    const words = mnemonic.trim().split(/\s+/);
    if (words.length !== 24 && words.length !== 12) {
      setError("Seed phrase must be 12 or 24 words");
      return;
    }
    setError("");
    setLoading(true);
    try {
      await importWallet(mnemonic, password);
      const redirect = pendingRedirect;
      handleClose();
      if (redirect) router.push(redirect);
    } catch (e: any) {
      setError(e.message || "Invalid seed phrase");
    }
    setLoading(false);
  };

  const handleUnlock = async () => {
    if (!password) {
      setError("Enter your password");
      return;
    }
    setError("");
    setLoading(true);
    try {
      await unlock(password);

      // Check if relay grant exists — if not, show grant step.
      try {
        const relayInfo = await bff.relayInfo();
        if (relayInfo.enabled && relayInfo.relayAddress && address) {
          const hasGrant = await checkRelayGrant(address, relayInfo.relayAddress);
          if (!hasGrant) {
            // Fetch balance to decide: faucet+grant or grant only
            try {
              const b = await bff.balance(address);
              setBalance(getUSDCBalance(b.balances));
            } catch {}
            setFaucetStep(true);
            setFaucetMsg("");
            setLoading(false);
            return; // don't close modal — show grant step
          }
        }
      } catch {}

      const redirect = pendingRedirect;
      handleClose();
      if (redirect) router.push(redirect);
    } catch {
      setError("Wrong password");
    }
    setLoading(false);
  };

  const handleCopy = () => {
    navigator.clipboard.writeText(generatedMnemonic);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleCopyAddr = () => {
    if (!address) return;
    navigator.clipboard.writeText(address);
    setCopiedAddr(true);
    setTimeout(() => setCopiedAddr(false), 2000);
  };

  const handleFaucet = async () => {
    if (!address) return;
    setFaucetLoading(true);
    setFaucetMsg("");
    try {
      await bff.faucet(address);
      setFaucetMsg("Tokens sent!");
      // Refresh balance after a short delay.
      setTimeout(() => {
        bff.balance(address).then(b => setBalance(getUSDCBalance(b.balances))).catch(() => {});
      }, 2000);
      // Ensure relay grant exists (re-creates after devnet restart).
      if (wallet) {
        await ensureRelayGrant(wallet, address);
      }
    } catch {
      setFaucetMsg("Faucet error");
    }
    setFaucetLoading(false);
  };

  const handleLock = () => {
    lock();
    handleClose();
  };

  const handleDisconnect = () => {
    if (!confirmDelete) {
      setConfirmDelete(true);
      return;
    }
    if (deleteInput === "DELETE") {
      clear();
      handleClose();
      setConfirmDelete(false);
      setDeleteInput("");
    }
  };

  const handleMnemonicSavedDone = () => {
    setGeneratedMnemonic("");
    setFaucetStep(true);
  };

  const handleFaucetAndGo = async () => {
    if (!address || !wallet) return;
    setFaucetLoading(true);
    setFaucetMsg("");
    try {
      // 1. Get USDC from faucet.
      await bff.faucet(address);
      setFaucetMsg("Tokens sent! Authorizing instant bets...");

      // 2. Wait for balance to arrive.
      for (let i = 0; i < 10; i++) {
        await new Promise(r => setTimeout(r, 1000));
        try {
          const b = await bff.balance(address);
          const bal = Number(getUSDCBalance(b.balances));
          if (bal > 0) break;
        } catch {}
      }

      // 3. Grant authz to relay (one-time, player signs once).
      try {
        const relayInfo = await bff.relayInfo();
        if (relayInfo.enabled && relayInfo.relayAddress) {
          const grantResult = await grantRelay(wallet, address, relayInfo.relayAddress);
          if (grantResult.code === 0) {
            setFaucetMsg("Ready! Redirecting...");
          } else {
          }
        }
      } catch (e) {
        // Non-fatal — player can still play with direct signing.
      }

      await new Promise(r => setTimeout(r, 500));
      const redirect = pendingRedirect;
      reset();
      closeModal();
      if (redirect) router.push(redirect);
    } catch {
      setFaucetMsg("Faucet error — try again");
    }
    setFaucetLoading(false);
  };

  /* ── Golden accent color used throughout ── */
  const gold = "#fde48b";
  const goldBright = "#facc15";

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/80 backdrop-blur-md px-4">
      <div className="w-full max-w-md rounded-2xl border border-[#fde48b]/20 bg-[#0d0f14] p-6 shadow-[0_0_60px_rgba(212,184,96,0.08)] font-[family-name:var(--font-display)]">
        {/* Header */}
        <div className="flex items-center justify-between mb-1">
          <h2 className="text-xl font-black text-white">
            {generatedMnemonic
              ? "Save Your Seed Phrase"
              : faucetStep
              ? (Number(balance) / 1e6 < 1 ? "You're Almost Ready" : "Authorize Instant Betting")
              : "ExoWallet"}
          </h2>
          <button
            onClick={handleClose}
            className="text-gray-500 hover:text-white transition-colors cursor-pointer"
          >
            <X className="w-5 h-5" />
          </button>
        </div>
        {status === "none" && !generatedMnemonic && !faucetStep && (
          <div className="flex items-center gap-3 mb-5">
            <p className="text-[11px] text-[#8a8070]">Encrypted in your browser. Ready in 5 seconds.</p>
            <span className="shrink-0 inline-flex items-center gap-1 px-2 py-0.5 rounded-full border border-emerald-500/30 bg-emerald-500/10 text-[9px] text-emerald-400 font-bold tracking-wider uppercase"><Shield className="w-3 h-3" />Local</span>
          </div>
        )}
        {status === "locked" && !generatedMnemonic && !faucetStep && (
          <p className="text-[11px] text-[#8a8070] mb-5">Enter your password to unlock.</p>
        )}
        {!faucetStep && status === "unlocked" && !generatedMnemonic && <div className="mb-4" />}
        {!faucetStep && generatedMnemonic && <div className="mb-4" />}

        {/* ── UNLOCKED: wallet info ── */}
        {status === "unlocked" && !generatedMnemonic && !faucetStep ? (
          <div className="space-y-4">
            {/* Address */}
            <div className="rounded-lg bg-[#07080b] border border-[#fde48b]/15 p-4">
              <div className="flex items-center justify-between">
                <span className="font-mono text-sm text-white">{address?.slice(0, 10)}...{address?.slice(-6)}</span>
                <button onClick={handleCopyAddr}
                  className="shrink-0 text-[#fde48b]/60 hover:text-[#fde48b] transition-colors cursor-pointer" title="Copy address">
                  {copiedAddr ? <Check className="w-4 h-4 text-emerald-400" /> : <Copy className="w-4 h-4" />}
                </button>
              </div>
              <a href={`${process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io"}/bets?bettor=${address}`} target="_blank" rel="noopener noreferrer"
                className="text-[11px] text-[#fde48b]/70 hover:text-[#fde48b] transition-colors mt-2 inline-block cursor-pointer">
                View in Block Explorer →
              </a>
            </div>

            {/* Balance */}
            <div className="rounded-lg bg-[#07080b] border border-[#fde48b]/15 p-4">
              <div className="text-[10px] text-[#8a8070] uppercase tracking-[2px] font-bold mb-1">Balance</div>
              <div className="text-2xl font-black text-white">${formatUSDC(balance)} <span className="text-sm text-[#8a8070] font-normal">USDC</span></div>
            </div>

            {/* Faucet — show when balance < $5 */}
            {(() => {
              const bal = Number(balance) / 1e6;
              if (bal >= 5) return null;
              return (
                <>
                  <button onClick={handleFaucet} disabled={faucetLoading}
                    className="w-full flex items-center justify-center gap-2 px-4 py-3 rounded-lg bg-[#fde48b]/10 border border-[#fde48b]/25 text-[#fde48b] text-sm font-bold hover:bg-[#fde48b]/20 transition-colors disabled:opacity-50 cursor-pointer">
                    <Droplets className="w-4 h-4" />
                    {faucetLoading ? "Sending..." : bal === 0 ? "Get Test USDC" : "Get More USDC"}
                  </button>
                  {faucetMsg && (
                    <p className={`text-xs text-center ${faucetMsg.includes("error") ? "text-red-400" : "text-emerald-400"}`}>
                      {faucetMsg}
                    </p>
                  )}
                </>
              );
            })()}

            {/* Actions */}
            <div className="flex gap-2 pt-2">
              <button onClick={handleLock}
                className="flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg border border-white/10 text-gray-300 text-xs font-bold hover:text-white hover:border-white/20 transition-colors cursor-pointer">
                <LogOut className="w-3.5 h-3.5" /> Lock
              </button>
              <button onClick={() => setConfirmDelete(true)}
                className="flex-1 flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg border border-red-500/20 text-red-400/60 text-xs font-bold hover:text-red-400 hover:border-red-500/40 transition-colors cursor-pointer">
                <Trash2 className="w-3.5 h-3.5" /> Remove
              </button>
            </div>
            {confirmDelete && (
              <div className="border border-red-500/30 rounded-lg p-3 bg-red-500/5">
                <p className="text-[11px] text-red-400 mb-2">Save your seed phrase first. Type <strong>DELETE</strong> to confirm.</p>
                <div className="flex gap-2">
                  <input value={deleteInput} onChange={e => setDeleteInput(e.target.value)}
                    placeholder="DELETE"
                    className="flex-1 px-3 py-1.5 rounded bg-[#07080b] border border-red-500/20 text-sm text-white placeholder-gray-600 outline-none focus:border-red-500/50" />
                  <button onClick={handleDisconnect} disabled={deleteInput !== "DELETE"}
                    className="px-3 py-1.5 rounded bg-red-500/20 text-red-400 text-xs font-bold disabled:opacity-30 hover:bg-red-500/30 transition-colors cursor-pointer">
                    Confirm
                  </button>
                </div>
              </div>
            )}
          </div>

        /* ── MNEMONIC DISPLAY (after creation) ── */
        ) : generatedMnemonic ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-[#fde48b]/30 bg-[#fde48b]/5 p-4">
              <p className="text-[#fde48b] text-xs font-bold mb-3">
                Write this down — you need it to recover your wallet
              </p>
              <div className="bg-[#07080b] rounded p-3 font-mono text-xs text-white leading-relaxed select-all break-all">
                {generatedMnemonic}
              </div>
              <button
                onClick={handleCopy}
                className="mt-2 flex items-center gap-1 text-xs text-[#fde48b]/60 hover:text-[#fde48b] transition-colors"
              >
                {copied ? (
                  <><Check className="w-3 h-3" /> Copied</>
                ) : (
                  <><Copy className="w-3 h-3" /> Copy to clipboard</>
                )}
              </button>
            </div>
            <label className="flex items-center gap-2 cursor-pointer">
              <input
                type="checkbox"
                checked={mnemonicSaved}
                onChange={(e) => setMnemonicSaved(e.target.checked)}
                className="w-4 h-4 accent-[#fde48b]"
              />
              <span className="text-xs text-[#8a8070]">
                I have saved my seed phrase
              </span>
            </label>
            <button
              onClick={handleMnemonicSavedDone}
              disabled={!mnemonicSaved}
              className="w-full px-4 py-3 rounded-lg bg-[#facc15] text-[#0a0c10] text-sm font-bold hover:bg-[#fde047] transition-colors disabled:opacity-30 disabled:cursor-not-allowed"
            >
              Continue
            </button>
          </div>

        /* ── FAUCET / GRANT STEP ── */
        ) : faucetStep ? (
          <div className="space-y-4">
            {Number(balance) / 1e6 < 1 ? (
              <>
                <p className="text-[11px] text-[#8a8070]">Grab free test USDC to start playing.</p>
                <button onClick={handleFaucetAndGo} disabled={faucetLoading}
                  className="w-full flex items-center justify-center gap-2 px-4 py-3 rounded-lg bg-[#facc15] text-[#0a0c10] text-sm font-bold hover:bg-[#fde047] transition-colors disabled:opacity-50 cursor-pointer">
                  <Droplets className="w-4 h-4" />
                  {faucetLoading ? "Setting up..." : "Get Test USDC"}
                </button>
              </>
            ) : (
              <>
                <p className="text-[11px] text-[#8a8070]">Authorize instant betting — one-time signature, no per-bet approvals.</p>
                <button onClick={async () => {
                  if (!wallet || !address) return;
                  setFaucetLoading(true);
                  setFaucetMsg("");
                  try {
                    const relayInfo = await bff.relayInfo();
                    if (relayInfo.enabled && relayInfo.relayAddress) {
                      const res = await grantRelay(wallet, address, relayInfo.relayAddress);
                      if (res.code === 0) {
                        const redirect = pendingRedirect;
                        reset();
                        closeModal();
                        if (redirect) router.push(redirect);
                        return;
                      }
                      setFaucetMsg("Authorization failed. Try again.");
                    }
                  } catch (e: any) {
                    setFaucetMsg(e.message?.slice(0, 100) || "Authorization failed");
                  }
                  setFaucetLoading(false);
                }} disabled={faucetLoading}
                  className="w-full flex items-center justify-center gap-2 px-4 py-3 rounded-lg bg-[#facc15] text-[#0a0c10] text-sm font-bold hover:bg-[#fde047] transition-colors disabled:opacity-50 cursor-pointer">
                  <Shield className="w-4 h-4" />
                  {faucetLoading ? "Authorizing..." : "Authorize Instant Betting"}
                </button>
              </>
            )}
            {faucetMsg && (
              <p className={`text-xs text-center ${faucetMsg.includes("error") ? "text-red-400" : "text-emerald-400"}`}>
                {faucetMsg}
              </p>
            )}
          </div>

        /* ── LOCKED: password unlock ── */
        ) : status === "locked" ? (
          <div className="space-y-4">
            <div className="relative">
              <input
                type="text" autoComplete="off" data-1p-ignore data-lpignore="true" style={showPassword ? {} : { WebkitTextSecurity: "disc" } as any}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && handleUnlock()}
                placeholder="Enter password"
                className="w-full pr-10 px-4 py-3 rounded-lg bg-[#07080b] border border-[#fde48b]/20 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-[#facc15]/60 focus:shadow-[0_0_0_1px_rgba(250,204,21,0.15)] transition-all"
              />
              <button
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-white"
              >
                {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>
            {error && <p className="text-xs text-red-400">{error}</p>}
            <button
              onClick={handleUnlock}
              disabled={loading}
              className="w-full px-4 py-3 rounded-lg bg-[#facc15] text-[#0a0c10] text-sm font-bold hover:bg-[#fde047] transition-colors disabled:opacity-50"
            >
              {loading ? "Unlocking..." : "Unlock"}
            </button>
          </div>

        /* ── NO WALLET: create / import ── */
        ) : (
          <div className="space-y-4">
            <div className="flex gap-2">
              <button
                onClick={() => { setTab("create"); setError(""); }}
                className={`flex-1 py-2 rounded-lg text-xs font-bold transition-colors ${
                  tab === "create"
                    ? "bg-[#fde48b]/10 text-[#fde48b] border border-[#fde48b]/30"
                    : "text-gray-500 border border-white/5 hover:text-white"
                }`}
              >
                Create New
              </button>
              <button
                onClick={() => { setTab("import"); setError(""); }}
                className={`flex-1 py-2 rounded-lg text-xs font-bold transition-colors ${
                  tab === "import"
                    ? "bg-[#fde48b]/10 text-[#fde48b] border border-[#fde48b]/30"
                    : "text-gray-500 border border-white/5 hover:text-white"
                }`}
              >
                Import Wallet
              </button>
            </div>

            {tab === "import" && (
              <textarea
                value={mnemonic}
                onChange={(e) => setMnemonic(e.target.value)}
                placeholder="Enter your 24-word seed phrase"
                rows={3}
                className="w-full px-4 py-3 rounded-lg bg-[#07080b] border border-[#fde48b]/20 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-[#facc15]/60 focus:shadow-[0_0_0_1px_rgba(250,204,21,0.15)] transition-all resize-none font-mono"
              />
            )}

            <div className="relative">
              <input
                type="text" autoComplete="off" data-1p-ignore data-lpignore="true" style={showPassword ? {} : { WebkitTextSecurity: "disc" } as any}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Set password (min 3 characters)"
                className="w-full pr-10 px-4 py-3 rounded-lg bg-[#07080b] border border-[#fde48b]/20 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-[#facc15]/60 focus:shadow-[0_0_0_1px_rgba(250,204,21,0.15)] transition-all"
              />
              <button
                onClick={() => setShowPassword(!showPassword)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-white"
              >
                {showPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>

            {tab === "create" && (
              <input
                type="text" autoComplete="off" data-1p-ignore data-lpignore="true" style={showPassword ? {} : { WebkitTextSecurity: "disc" } as any}
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                placeholder="Confirm password"
                className="w-full px-4 py-3 rounded-lg bg-[#07080b] border border-[#fde48b]/20 text-sm text-white placeholder-gray-600 focus:outline-none focus:border-[#facc15]/60 focus:shadow-[0_0_0_1px_rgba(250,204,21,0.15)] transition-all"
              />
            )}

            {error && <p className="text-xs text-red-400">{error}</p>}

            <p className="text-xs text-zinc-400 text-center flex items-center justify-center gap-1.5"><Shield className="w-3 h-3" />Your keys never leave this browser</p>
            <button
              onClick={tab === "create" ? handleCreate : handleImport}
              disabled={loading}
              className="w-full px-4 py-3 rounded-lg bg-[#facc15] text-[#0a0c10] text-sm font-bold hover:bg-[#fde047] transition-colors disabled:opacity-50"
            >
              {loading
                ? "Working..."
                : tab === "create"
                ? "Let's Go"
                : "Import & Play"}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
