"use client";

import { X } from "lucide-react";

interface InfoModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

export function InfoModal({ open, onClose, title, children }: InfoModalProps) {
  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/70 backdrop-blur-sm"
        onClick={onClose}
      />

      {/* Modal */}
      <div className="relative w-full max-w-md bg-[#0d0f15] border border-white/10 rounded-2xl p-6 shadow-2xl">
        {/* Close button */}
        <button
          onClick={onClose}
          className="absolute top-4 right-4 text-gray-500 hover:text-gray-300 transition-colors"
        >
          <X className="w-5 h-5" />
        </button>

        {/* Title */}
        <h2 className="text-sm font-bold text-white tracking-widest uppercase mb-4">
          {title}
        </h2>

        {/* Content */}
        <div className="text-sm text-gray-400 leading-relaxed">
          {children}
        </div>

        {/* Close action */}
        <button
          onClick={onClose}
          className="mt-6 w-full py-2.5 rounded-lg bg-white/5 border border-white/10 text-xs text-gray-300 font-bold tracking-widest uppercase hover:bg-white/10 transition-colors"
        >
          OK
        </button>
      </div>
    </div>
  );
}
