import type { ReactNode } from "react";

export function PanelCount({ children }: { children: ReactNode }) {
  return (
    <span className="shrink-0 font-mono text-[11px] text-mute">{children}</span>
  );
}
