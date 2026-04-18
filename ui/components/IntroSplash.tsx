"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import styles from "./IntroSplash.module.css";

export const INTRO_STORAGE_KEY = "exohash_intro_seen";

export function IntroSplash() {
  const router = useRouter();
  const [fading, setFading] = useState(false);

  function dismiss() {
    if (fading) return;
    setFading(true);
    try { localStorage.setItem(INTRO_STORAGE_KEY, "1"); } catch {}
    setTimeout(() => router.replace("/"), 380);
  }

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " " || e.key === "Escape") dismiss();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [fading]);

  return (
    <div className={`${styles.root} ${fading ? styles.fading : ""}`}>
      <button className={styles.skip} onClick={dismiss}>SKIP →</button>
      <div className={styles.content}>
        <div className={styles.logo}>EXOHASH</div>
        <div className={styles.headline}>
          THE FIRST BLOCKCHAIN<br />BUILT FOR GAMBLING
        </div>
        <div className={styles.taglines}>
          <p>YOUR WALLET. YOUR KEYS. YOUR MONEY.</p>
          <p>NO DEPOSITS. NO SIGNUPS. NO BULLSHIT.</p>
          <p>THE HOUSE CAN'T CHEAT. THE MATH IS OPEN.</p>
        </div>
        <button className={styles.enterBtn} onClick={dismiss}>ENTER</button>
      </div>
    </div>
  );
}
