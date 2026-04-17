export function Footer() {
  return (
    <footer className="relative z-10 border-t border-purple-500/10 py-6 bg-black/70">
      <div className="mx-auto max-w-[1340px] px-6 flex flex-col sm:flex-row items-center justify-between gap-3">
        <span className="text-[10px] text-zinc-400 tracking-[0.2em] font-[family-name:var(--font-display)]">
          &copy; 2026 EXOHASH PROTOCOL
        </span>
        <div className="flex flex-wrap justify-center gap-x-5 gap-y-2">
          {[
            { label: "EXOHASH.IO", href: "https://exohash.io" },
            { label: "FAQ", href: "https://exohash.io/faq" },
            { label: "EXOSCAN", href: process.env.NEXT_PUBLIC_SCAN_URL || "https://devnet.exohash.io" },
            { label: "TELEGRAM", href: "https://t.me/ExohashHQ" },
            { label: "X", href: "https://x.com/ExoHashIO" },
          ].map((l) => (
            <a
              key={l.label}
              href={l.href}
              target="_blank"
              rel="noreferrer"
              className="text-[10px] text-zinc-400 hover:text-yellow-400 transition-colors tracking-[0.2em] font-[family-name:var(--font-display)]"
            >
              {l.label}
            </a>
          ))}
        </div>
      </div>
      <div className="text-center mt-4">
        <a
          href="https://t.me/ExohashHQ"
          target="_blank"
          rel="noreferrer"
          className="text-[12px] font-bold text-yellow-400 hover:text-yellow-300 transition-colors cursor-pointer underline-offset-4 hover:underline"
        >
          DevNet — Found a bug? Please report →
        </a>
      </div>
    </footer>
  );
}
