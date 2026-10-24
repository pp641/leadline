"use client";

import { useState } from "react";
import { FlaskConical, RotateCcw, X } from "lucide-react";
import { MOCK_MODE } from "@/lib/mock/config";

/** Floating notice shown on every page in demo mode, with a reset button. */
export function DemoBanner() {
  const [open, setOpen] = useState(true);
  if (!MOCK_MODE) return null;
  const reset = async () => {
    if (!confirm("Reset the demo? All changes you made in this browser are discarded.")) return;
    (await import("@/lib/mock")).resetDB();
    window.location.assign("/");
  };
  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} aria-label="Show demo mode notice"
        className="fixed right-4 bottom-4 z-50 flex size-9 items-center justify-center rounded-full bg-amber-500 text-white shadow-lg">
        <FlaskConical className="size-4" />
      </button>
    );
  }
  return (
    <div role="status" className="fixed right-4 bottom-4 z-50 flex max-w-[calc(100vw-2rem)] items-center gap-3 rounded-full border border-amber-500/40 bg-amber-50 py-1.5 pr-1.5 pl-3 text-sm text-amber-950 shadow-lg dark:bg-amber-950 dark:text-amber-50">
      <FlaskConical className="size-4 shrink-0 text-amber-600" />
      <span className="truncate"><b>Demo mode</b> · simulated data, stored in this browser</span>
      <button type="button" onClick={reset} className="flex shrink-0 items-center gap-1 rounded-full bg-amber-500/15 px-2.5 py-1 text-xs font-medium hover:bg-amber-500/25">
        <RotateCcw className="size-3" /> Reset
      </button>
      <button type="button" onClick={() => setOpen(false)} aria-label="Hide" className="rounded-full p-1 hover:bg-amber-500/15"><X className="size-3.5" /></button>
    </div>
  );
}
