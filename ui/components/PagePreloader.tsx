"use client";

export function PagePreloader({ message }: { message?: string }) {
  return (
    <div className="min-h-screen flex flex-col items-center justify-center page-bg relative gap-4 text-zinc-500">
      {message ? (
        <div className="glass-panel rounded-2xl px-6 py-4 text-center max-w-sm">
          <p className="text-lg text-white font-bold font-[family-name:var(--font-display)]">{message}</p>
        </div>
      ) : (
        <span>Loading...</span>
      )}
    </div>
  );
}
